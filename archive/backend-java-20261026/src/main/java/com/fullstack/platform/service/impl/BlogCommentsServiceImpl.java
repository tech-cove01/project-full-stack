package com.fullstack.platform.service.impl;

import com.fullstack.platform.entity.BlogComments;
import com.fullstack.platform.mapper.BlogCommentsMapper;
import com.fullstack.platform.service.IBlogCommentsService;
import com.baomidou.mybatisplus.spring.service.impl.ServiceImpl;
import org.springframework.stereotype.Service;

/**
 * <p>
 *  服务实现类
 * </p>
 *
 * @author 虎哥
 * @since 2021-12-22
 */
@Service
public class BlogCommentsServiceImpl extends ServiceImpl<BlogCommentsMapper, BlogComments> implements IBlogCommentsService {

}
