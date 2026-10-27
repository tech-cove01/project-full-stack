package com.fullstack.platform.ai;

import com.fullstack.platform.dto.Result;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/ai")
public class AiController {
    private final AiCapabilityService capabilities;
    private final AiKnowledgeService knowledge;

    public AiController(AiCapabilityService capabilities, AiKnowledgeService knowledge) {
        this.capabilities = capabilities;
        this.knowledge = knowledge;
    }

    @GetMapping("/capabilities")
    public Result capabilities() {
        return Result.ok(capabilities.capabilities());
    }

    @GetMapping("/knowledge/{id}")
    public Result publicDocument(@PathVariable("id") long id,
                                 @RequestParam(value = "version", required = false) Integer version) {
        return Result.ok(knowledge.publicDocument(id, version));
    }
}
