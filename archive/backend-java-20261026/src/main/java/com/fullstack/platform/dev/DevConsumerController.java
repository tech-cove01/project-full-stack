package com.fullstack.platform.dev;

import com.fullstack.platform.dto.Result;
import com.fullstack.platform.service.impl.VoucherOrderServiceImpl;
import org.springframework.context.annotation.Profile;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

/** Dev-only switch used to prove that accepted Stream messages survive a consumer restart. */
@Profile("dev")
@RestController
@RequestMapping("/dev/consumer")
public class DevConsumerController {
    private final VoucherOrderServiceImpl orders;

    public DevConsumerController(VoucherOrderServiceImpl orders) {
        this.orders = orders;
    }

    @PostMapping("/pause")
    public Result pause() {
        orders.pauseConsumer();
        return Result.ok();
    }

    @PostMapping("/resume")
    public Result resume() {
        orders.resumeConsumer();
        return Result.ok();
    }
}
