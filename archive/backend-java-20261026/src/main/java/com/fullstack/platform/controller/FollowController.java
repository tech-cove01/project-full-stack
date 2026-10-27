package com.fullstack.platform.controller;


import com.fullstack.platform.dto.Result;
import com.fullstack.platform.service.IFollowService;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.http.HttpStatus;

import jakarta.annotation.Resource;

/**
 * <p>
 * 前端控制器
 * </p>
 *
 * @author 虎哥
 */
@RestController
@RequestMapping("/follow")
public class FollowController {

    @Resource
    private IFollowService followService;

    @PutMapping("/{id}/{isFollow}")
    public Result follow(@PathVariable("id") Long followUserId, @PathVariable("isFollow") Boolean isFollow) {
        throw new ResponseStatusException(HttpStatus.FORBIDDEN, "社区写接口暂未开放");
    }

    @GetMapping("/or/not/{id}")
    public Result isFollow(@PathVariable("id") Long followUserId) {
        return followService.isFollow(followUserId);
    }

    @GetMapping("/common/{id}")
    public Result followCommons(@PathVariable("id") Long id){
        return followService.followCommons(id);
    }
}
