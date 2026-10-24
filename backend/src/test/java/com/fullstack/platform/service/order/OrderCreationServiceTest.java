package com.fullstack.platform.service.order;

import com.baomidou.mybatisplus.core.conditions.Wrapper;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.SeckillVoucherMapper;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.mockito.Mock;
import org.mockito.junit.jupiter.MockitoExtension;
import org.springframework.aop.framework.ProxyFactory;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.annotation.AnnotationTransactionAttributeSource;
import org.springframework.transaction.interceptor.TransactionInterceptor;
import org.springframework.transaction.support.SimpleTransactionStatus;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

@ExtendWith(MockitoExtension.class)
class OrderCreationServiceTest {
    @Mock VoucherOrderMapper orders;
    @Mock SeckillVoucherMapper vouchers;
    OrderCreationService writer;

    @BeforeEach
    void setUp() {
        writer = new OrderCreationService(orders, vouchers);
    }

    private VoucherOrder order() {
        return new VoucherOrder().setId(100L).setUserId(7L).setVoucherId(20L);
    }

    @Test
    void committedMessageRedeliveryDoesNotDecrementStockOrInsertAgain() {
        when(orders.selectById(100L)).thenReturn(order());
        writer.create(order());
        verifyNoInteractions(vouchers);
        verify(orders, never()).insert(any(VoucherOrder.class));
    }

    @Test
    void anotherOrderForSameUserIsDefiniteRejectionAndKeepsPurchaseMarker() {
        when(orders.selectOne(any(Wrapper.class))).thenReturn(order().setId(99L));
        OrderRejectedException rejection = assertThrows(OrderRejectedException.class, () -> writer.create(order()));
        assertTrue(rejection.keepPurchaseMarker());
        verifyNoInteractions(vouchers);
        verify(orders, never()).insert(any(VoucherOrder.class));
    }

    @Test
    void failedConditionalStockUpdateDoesNotInsertOrder() {
        OrderRejectedException rejection = assertThrows(OrderRejectedException.class, () -> writer.create(order()));
        assertFalse(rejection.keepPurchaseMarker());
        verify(orders, never()).insert(any(VoucherOrder.class));
    }

    @Test
    void duplicateInsertRollsBackStockTransactionThroughSpringProxy() {
        PlatformTransactionManager transactions = mock(PlatformTransactionManager.class);
        SimpleTransactionStatus transaction = new SimpleTransactionStatus();
        when(transactions.getTransaction(any())).thenReturn(transaction);
        when(vouchers.update(isNull(), any(Wrapper.class))).thenReturn(1);
        when(orders.insert(any(VoucherOrder.class))).thenThrow(new DuplicateKeyException("one user, one voucher"));
        OrderCreationService proxy = transactionalProxy(transactions);

        assertThrows(DuplicateKeyException.class, () -> proxy.create(order()));
        var sequence = inOrder(vouchers, orders, transactions);
        sequence.verify(vouchers).update(isNull(), any(Wrapper.class));
        sequence.verify(orders).insert(any(VoucherOrder.class));
        sequence.verify(transactions).rollback(transaction);
        verify(transactions, never()).commit(any());
    }

    @Test
    void successfulOrderCommitsOnlyAfterStockAndInsert() {
        PlatformTransactionManager transactions = mock(PlatformTransactionManager.class);
        SimpleTransactionStatus transaction = new SimpleTransactionStatus();
        when(transactions.getTransaction(any())).thenReturn(transaction);
        when(vouchers.update(isNull(), any(Wrapper.class))).thenReturn(1);
        when(orders.insert(any(VoucherOrder.class))).thenReturn(1);
        VoucherOrder order = order();

        transactionalProxy(transactions).create(order);
        assertEquals(1, order.getStatus());
        var sequence = inOrder(vouchers, orders, transactions);
        sequence.verify(vouchers).update(isNull(), any(Wrapper.class));
        sequence.verify(orders).insert(order);
        sequence.verify(transactions).commit(transaction);
        verify(transactions, never()).rollback(any());
    }

    @Test
    void identifierCollisionCannotBeTreatedAsMessageRedelivery() {
        when(orders.selectById(100L)).thenReturn(order().setUserId(8L));
        assertThrows(IllegalStateException.class, () -> writer.create(order()));
        verifyNoInteractions(vouchers);
    }

    private OrderCreationService transactionalProxy(PlatformTransactionManager transactions) {
        ProxyFactory factory = new ProxyFactory(writer);
        factory.setProxyTargetClass(true);
        factory.addAdvice(new TransactionInterceptor(transactions, new AnnotationTransactionAttributeSource()));
        return (OrderCreationService) factory.getProxy();
    }
}
