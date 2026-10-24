package com.fullstack.platform.ai;

import lombok.extern.slf4j.Slf4j;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.stereotype.Component;

import java.util.List;

/** Stable public FAQ seeds; only published versions are eligible for retrieval. */
@Slf4j
@Component
@Profile("dev")
public class AiSeedInitializer implements ApplicationRunner {
    private final AiKnowledgeService knowledge;
    private final AiProvider provider;

    public AiSeedInitializer(AiKnowledgeService knowledge, AiProvider provider) {
        this.knowledge = knowledge;
        this.provider = provider;
    }

    @Override
    public void run(ApplicationArguments args) {
        List<Long> platform = List.of(
                seed("platform-payment-mock", "PLATFORM", null, "模拟支付说明",
                        "本项目使用模拟支付，不连接真实资金通道。支付成功只更新本地订单状态，不会产生真实扣款。"),
                seed("platform-async-order", "PLATFORM", null, "抢购受理与建单",
                        "抢购接口返回订单号只表示请求已受理，不代表数据库已经创建订单。请查看订单的 state 与 orderStatus；PROCESSING 表示处理中，CREATED 且 orderStatus=1 才表示订单已创建、待支付。"),
                seed("platform-pay-deadline", "PLATFORM", null, "支付期限与超时关单",
                        "未支付订单默认在创建后 15 分钟内支付，最终以订单快照中的 payDeadlineAt 为准。超过期限后系统会关闭订单并回补库存，过期订单不能支付。"),
                seed("platform-cancel-rebuy", "PLATFORM", null, "取消后是否可以重新购买",
                        "未支付订单可以取消，超时关单也视为取消；取消后库存释放，用户可以重新购买同一活动。已支付订单不能直接取消，只能由商家按规则退款。"),
                seed("platform-refund-rebuy", "PLATFORM", null, "退款后是否可以重新购买",
                        "已支付且未核销的订单可以由店主或管理者执行模拟退款。退款后库存回补，但购买资格保留，不能再次购买同一活动。已核销订单不能退款。"),
                seed("platform-redeem", "PLATFORM", null, "到店核销规则",
                        "核销只允许已支付、处于核销期内且由授权门店的店员或店主操作。核销需要用户订单详情中的核销码。重复核销不会产生第二次核销效果。"),
                seed("platform-sale-window", "PLATFORM", null, "销售期与核销期",
                        "销售期只控制新预约，核销期单独配置。活动下架或销售结束后不能再抢购，但已经受理的预约仍按原规则继续落单。"),
                seed("platform-publish-immutable", "PLATFORM", null, "活动发布后能否修改",
                        "活动发布后价格、库存、门店、销售时间和核销时间不可修改；需要调整时应下架后重新发布。下架后不会恢复上架。"),
                seed("platform-stock-restore", "PLATFORM", null, "库存回补规则",
                        "取消和退款最多各回补一次数据库库存和 Redis 库存。回补通过持久化事件重试，重复投递不会重复增加库存。"),
                seed("platform-ai-no-write", "PLATFORM", null, "AI 助手能做什么",
                        "AI 助手只能查询公开规则、公开活动信息和本人订单，不能下单、支付、取消、退款或核销。需要这些操作时请使用站内正常业务页面。"),
                seed("platform-order-status", "PLATFORM", null, "订单状态含义",
                        "orderStatus=1 待支付，2 已支付，3 已核销，4 已取消，6 已退款。state=PROCESSING/CREATED/FAILED 是异步处理状态，不能与业务状态混用。"),
                seed("platform-sensitive", "PLATFORM", null, "不要发送敏感信息",
                        "请勿在助手输入中发送手机号、验证码、登录 Token 或完整核销码。助手不会要求这些信息，也不会把它们发送给外部模型。")
        );

        List<Long> shops = List.of(
                seed("shop-a1-parking", "SHOP_PUBLIC", 1L, "街角咖啡停车规则",
                        "街角咖啡（门店 1）消费满 50 元可在地下 B2 车库免费停车 2 小时，超出部分按商场标准收费。"),
                seed("shop-a1-reservation", "SHOP_PUBLIC", 1L, "街角咖啡预约规则",
                        "街角咖啡周末建议提前一天电话预约；工作日 14:00 前通常可直接到店。"),
                seed("shop-a2-parking", "SHOP_PUBLIC", 2L, "青禾食堂停车规则",
                        "青禾食堂（门店 2）使用商场地面停车场，前 30 分钟免费，之后按次收费，不提供消费免停。"),
                seed("shop-a2-reservation", "SHOP_PUBLIC", 2L, "青禾食堂预约规则",
                        "青禾食堂工作日午市不接受电话预留，可直接到店排队；包间需要提前一天确认。"),
                seed("shop-b1-events", "SHOP_PUBLIC", 3L, "城市书房活动规则",
                        "城市书房每月 8 日会员日，教辅区不参与折扣；签售活动需要在书店公众号报名，名额有限。"),
                seed("shop-b1-quiet", "SHOP_PUBLIC", 3L, "城市书房安静阅读区",
                        "城市书房二层为安静阅读区，请将手机调至静音；儿童阅读区位于一层，周末有绘本活动。")
        );

        if (provider.ready()) {
            platform.forEach(knowledge::publishSeedIfReady);
            shops.forEach(knowledge::publishSeedIfReady);
            log.info("AI seed publication attempted with provider mode {}", provider.mode());
        } else {
            log.info("AI disabled/not ready ({}); seed documents kept as drafts", provider.reason());
        }
    }

    private long seed(String key, String scope, Long shopId, String title, String body) {
        return knowledge.ensureSeed(key, scope, shopId, title, body);
    }
}
