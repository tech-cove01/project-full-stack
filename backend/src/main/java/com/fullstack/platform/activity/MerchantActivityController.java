package com.fullstack.platform.activity;

import com.fullstack.platform.dto.Result;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/merchant/activities")
public class MerchantActivityController {
    private final MerchantActivityService service;

    public MerchantActivityController(MerchantActivityService service) {
        this.service = service;
    }

    @PostMapping
    public Result create(@RequestHeader("Idempotency-Key") String key,
                         @RequestBody CreateActivityRequest request) {
        return Result.ok(service.create(key, request));
    }

    @GetMapping
    public Result list(@RequestParam(value = "current", defaultValue = "1") int current,
                       @RequestParam(value = "pageSize", defaultValue = "10") int pageSize,
                       @RequestParam(value = "shopId", required = false) String shopId,
                       @RequestParam(value = "status", required = false) String status) {
        MerchantActivityService.Page page = service.list(current, pageSize, shopId, status);
        return Result.ok(page.items(), page.total());
    }

    @PostMapping("/{id}/retry-publication")
    public Result retry(@PathVariable("id") long id) {
        return Result.ok(service.retryPublication(id));
    }

    @PostMapping("/{id}/offline")
    public Result offline(@PathVariable("id") long id) {
        return Result.ok(service.offline(id));
    }
}
