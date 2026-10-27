package com.fullstack.platform.ai;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.scheduling.concurrent.ThreadPoolTaskExecutor;

@Configuration
public class AiExecutorConfig {
    @Bean(name = "aiExecutor")
    public ThreadPoolTaskExecutor aiExecutor(AiProperties properties) {
        ThreadPoolTaskExecutor executor = new ThreadPoolTaskExecutor();
        int size = Math.max(1, properties.getMaxConcurrent());
        executor.setCorePoolSize(size);
        executor.setMaxPoolSize(size);
        executor.setQueueCapacity(20);
        executor.setThreadNamePrefix("ai-");
        executor.setWaitForTasksToCompleteOnShutdown(true);
        executor.setAwaitTerminationSeconds(5);
        executor.initialize();
        return executor;
    }
}
