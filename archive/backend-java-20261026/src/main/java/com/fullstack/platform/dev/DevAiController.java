package com.fullstack.platform.dev;

import com.fullstack.platform.ai.AiProvider;
import com.fullstack.platform.ai.StubAiProvider;
import com.fullstack.platform.dto.Result;
import org.springframework.context.annotation.Profile;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.Map;

/**
 * Dev-only counters used to prove that a rejected input never reached embedding or chat.
 * Only the stub provider exposes counters; a real provider reports "unavailable".
 */
@Profile("dev")
@RestController
@RequestMapping("/dev/ai")
public class DevAiController {
    private final AiProvider provider;

    public DevAiController(AiProvider provider) {
        this.provider = provider;
    }

    @GetMapping("/calls")
    public Result calls() {
        if (provider instanceof StubAiProvider stub) {
            return Result.ok(Map.of("provider", "stub", "embed", stub.embedCalls(),
                    "chat", stub.chatCalls()));
        }
        return Result.ok(Map.of("provider", provider.mode(), "counter", "unavailable"));
    }
}
