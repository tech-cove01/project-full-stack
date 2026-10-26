package com.fullstack.platform.service.impl;

import com.fullstack.platform.dto.OrderStatusDTO;
import com.fullstack.platform.dto.Result;
import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.order.OrderReviewRepository;
import com.fullstack.platform.service.ISeckillVoucherService;
import com.fullstack.platform.service.order.OrderCreationService;
import com.fullstack.platform.utils.RedisIdWorker;
import com.fullstack.platform.utils.UserHolder;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import org.springframework.dao.DataAccessResourceFailureException;
import org.springframework.data.redis.connection.stream.MapRecord;
import org.springframework.data.redis.connection.stream.RecordId;
import org.springframework.data.redis.connection.stream.StreamRecords;
import org.springframework.data.redis.core.HashOperations;
import org.springframework.data.redis.core.StreamOperations;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.RedisScript;

import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class VoucherOrderServiceImplTest {
    @Mock ISeckillVoucherService vouchers;
    @Mock RedisIdWorker ids;
    @Mock StringRedisTemplate redis;
    @Mock OrderCreationService writer;
    @Mock OrderReviewRepository reviews;
    @Mock HashOperations<String, Object, Object> hashes;
    @Mock StreamOperations<String, Object, Object> streams;
    VoucherOrderServiceImpl service;

    @BeforeEach
    void setUp() {
        service = new VoucherOrderServiceImpl(vouchers, ids, redis, writer, reviews);
        UserDTO user = new UserDTO();
        user.setId(7L);
        UserHolder.saveUser(user);
    }

    @AfterEach
    void tearDown() {
        UserHolder.removeUser();
        service.stopConsumer();
    }

    @Test
    void invalidActivityCannotEnqueueAnOrder() {
        Result result = service.seckillVoucher(20L);
        assertFalse(result.getSuccess());
        verifyNoInteractions(ids, redis);
    }

    @Test
    void futureActivityCannotEnqueueAnOrder() {
        LocalDateTime now = LocalDateTime.now(ZoneId.of("Asia/Shanghai"));
        when(vouchers.getById(20L)).thenReturn(new SeckillVoucher().setBeginTime(now.plusHours(1))
                .setEndTime(now.plusHours(2)).setPublishStatus("ACTIVE").setPublishVersion(1));
        assertEquals("活动尚未开始", service.seckillVoucher(20L).getErrorMsg());
        verifyNoInteractions(ids, redis);
    }

    @Test
    void completedActivityCannotEnqueueAnOrder() {
        LocalDateTime now = LocalDateTime.now(ZoneId.of("Asia/Shanghai"));
        when(vouchers.getById(20L)).thenReturn(new SeckillVoucher().setBeginTime(now.minusHours(2))
                .setEndTime(now.minusHours(1)).setPublishStatus("ACTIVE").setPublishVersion(1));
        assertEquals("活动已经结束", service.seckillVoucher(20L).getErrorMsg());
        verifyNoInteractions(ids, redis);
    }

    @Test
    void acceptedOrderReturnsStringIdWithoutClaimingDatabaseCreation() {
        LocalDateTime now = LocalDateTime.now(ZoneId.of("Asia/Shanghai"));
        when(vouchers.getById(20L)).thenReturn(new SeckillVoucher().setBeginTime(now.minusHours(1))
                .setEndTime(now.plusHours(1)).setPublishStatus("ACTIVE").setPublishVersion(1));
        when(ids.nextId("order")).thenReturn(9_007_199_254_740_993L);
        when(redis.execute(any(RedisScript.class), anyList(), any(Object[].class))).thenReturn(0L);
        Result result = service.seckillVoucher(20L);
        assertTrue(result.getSuccess());
        assertEquals("9007199254740993", result.getData());
        verifyNoInteractions(writer);
    }

    @Test
    void pendingReservationRequiresOwnerEvenBeforeAnOrderExists() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(hashes.entries("order:reservation:100")).thenReturn(Map.of("userId", "8", "state", "PROCESSING"));
        Result result = service.queryOrderStatus(100L);
        assertFalse(result.getSuccess());
        assertNull(result.getData());
    }

    @Test
    void ownerCanReadPendingStatus() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(hashes.entries("order:reservation:100")).thenReturn(Map.of("userId", "7", "state", "PROCESSING"));
        Result result = service.queryOrderStatus(100L);
        assertEquals(new OrderStatusDTO("100", "PROCESSING", "订单处理中", null), result.getData());
    }

    @Test
    void anotherUsersCreatedOrderIsNotExposed() {
        when(writer.findById(100L)).thenReturn(new VoucherOrder().setId(100L).setUserId(8L));
        assertFalse(service.queryOrderStatus(100L).getSuccess());
        verifyNoInteractions(redis);
    }

    @Test
    void acknowledgementUsesActualStreamAndOnlyFollowsTransactionalWriterAndStatus() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(redis.opsForStream()).thenReturn(streams);
        MapRecord<String, Object, Object> record = record();
        assertTrue(service.processRecord(record));
        var sequence = inOrder(writer, hashes, streams);
        sequence.verify(writer).create(argThat(order -> order.getId() == 100L && order.getUserId() == 7L));
        sequence.verify(hashes).putAll(eq("order:reservation:100"), argThat(map -> "CREATED".equals(map.get("state"))));
        sequence.verify(streams).acknowledge("stream.orders", "g1", record.getId());
    }

    @Test
    void transientDatabaseFailureRemainsPendingAndIsNotAcknowledged() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(hashes.increment("order:reservation:100", "retries", 1L)).thenReturn(1L);
        doThrow(new DataAccessResourceFailureException("database unavailable")).when(writer).create(any());
        assertFalse(service.processRecord(record()));
        verifyNoInteractions(streams);
        verify(redis, never()).opsForStream();
    }

    @Test
    void retriesAreBoundedAndUnknownOutcomeIsPreservedForReview() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(redis.opsForStream()).thenReturn(streams);
        when(hashes.increment("order:reservation:100", "retries", 1L)).thenReturn(5L);
        doThrow(new DataAccessResourceFailureException("unknown commit outcome")).when(writer).create(any());
        MapRecord<String, Object, Object> record = record();
        assertTrue(service.processRecord(record));
        var sequence = inOrder(reviews, streams, hashes);
        sequence.verify(reviews).record(any(VoucherOrder.class), eq(record.getId().getValue()), eq(5L), any());
        sequence.verify(streams).add(eq("stream.orders.dead"), argThat(map -> "100".equals(map.get("id"))));
        sequence.verify(hashes).putAll(eq("order:reservation:100"), argThat(map -> "true".equals(map.get("manualReview"))
                && "PROCESSING".equals(map.get("state"))));
        sequence.verify(streams).acknowledge("stream.orders", "g1", record.getId());
        verify(redis, never()).execute(any(RedisScript.class), anyList(), any(Object[].class));
    }

    @Test
    void messageStaysPendingWhenTheReviewRowCannotBePersisted() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(hashes.increment("order:reservation:100", "retries", 1L)).thenReturn(5L);
        doThrow(new DataAccessResourceFailureException("unknown commit outcome")).when(writer).create(any());
        doThrow(new DataAccessResourceFailureException("database still unavailable"))
                .when(reviews).record(any(VoucherOrder.class), any(), anyLong(), any());

        assertFalse(service.processRecord(record()));
        // No dead letter, no Redis-only acknowledgement: the Stream entry must stay pending.
        verify(redis, never()).opsForStream();
        verify(reviews, never()).openCount();
    }

    @Test
    void replayOfAlreadyRejectedMessageDoesNotCreateAnOrder() {
        when(redis.opsForHash()).thenReturn(hashes);
        when(redis.opsForStream()).thenReturn(streams);
        when(hashes.get("order:reservation:100", "state")).thenReturn("FAILED");
        assertTrue(service.processRecord(record()));
        verifyNoInteractions(writer);
        verify(streams).acknowledge(eq("stream.orders"), eq("g1"), any(RecordId.class));
    }

    private MapRecord<String, Object, Object> record() {
        return StreamRecords.newRecord().in("stream.orders")
                .ofMap(Map.<Object, Object>of("id", "100", "userId", "7", "voucherId", "20"))
                .withId(RecordId.of("1000-0"));
    }
}
