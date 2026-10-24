package com.fullstack.platform.controller;

import com.fullstack.platform.dto.Result;
import com.fullstack.platform.utils.UserHolder;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.multipart.MultipartFile;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.http.HttpStatus;
import javax.imageio.ImageIO;
import java.awt.image.BufferedImage;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Locale;
import java.util.Set;
import java.util.UUID;

@RestController
@RequestMapping("/upload")
public class UploadController {
    private final Path root;

    public UploadController(@Value("${app.upload-dir:./uploads}") String directory) {
        root = Path.of(directory).toAbsolutePath().normalize();
    }

    @PostMapping("/blog")
    public Result uploadImage(@RequestParam("file") MultipartFile file) throws IOException {
        String original = file.getOriginalFilename();
        String suffix = original != null && original.contains(".")
                ? original.substring(original.lastIndexOf('.') + 1).toLowerCase(Locale.ROOT) : "";
        if (file.isEmpty() || file.getSize() > 5 * 1024 * 1024 || !Set.of("jpg", "jpeg", "png", "gif").contains(suffix)) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "仅支持 5MB 以内的 JPG、PNG、GIF 图片");
        }
        BufferedImage decoded;
        try (var input = file.getInputStream()) { decoded = ImageIO.read(input); }
        if (decoded == null) throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "图片内容无效");
        Path directory = root.resolve(UserHolder.getUser().getId().toString());
        Files.createDirectories(directory);
        String filename = UUID.randomUUID() + "." + suffix;
        file.transferTo(directory.resolve(filename));
        return Result.ok("/uploads/" + directory.getFileName() + "/" + filename);
    }

    @DeleteMapping("/blog")
    public Result deleteBlogImg(@RequestParam("name") String name) throws IOException {
        String prefix = "/uploads/" + UserHolder.getUser().getId() + "/";
        if (!name.startsWith(prefix)) throw new ResponseStatusException(HttpStatus.FORBIDDEN, "仅可删除本人上传的图片");
        String filename = name.substring(prefix.length());
        if (!filename.matches("[a-f0-9-]{36}\\.(jpg|jpeg|png|gif)")) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "文件名无效");
        }
        Path userRoot = root.resolve(UserHolder.getUser().getId().toString());
        Path target = userRoot.resolve(filename).normalize();
        if (!target.startsWith(userRoot)) throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "文件路径无效");
        Files.deleteIfExists(target);
        return Result.ok();
    }
}
