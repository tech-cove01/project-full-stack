package com.fullstack.platform.merchant;

import com.fullstack.platform.dto.Result;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/merchant")
public class MerchantWorkspaceController {
    private final MerchantWorkspaceService service;

    public MerchantWorkspaceController(MerchantWorkspaceService service) {
        this.service = service;
    }

    @GetMapping("/workspace")
    public Result workspace() {
        return Result.ok(service.workspace());
    }
}
