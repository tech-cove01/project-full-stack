# Third-party source notices

The repository's MIT license applies to original contributions by this project's authors. It does not assign a new license to third-party source, documentation, or dependencies.

## Supplied backend reference

The initial backend was adapted from source supplied by the repository owner. Its original Git remote was `https://gitee.com/huyi612/hm-dianping.git`, at commit `a129c68` (`09-UV统计`). The supplied source identifies its author as 虎哥 in file comments. No repository-level license was present in that supplied snapshot, so no upstream MIT permission is asserted here.

The adapted files are the original controller/service/entity/mapper/utility classes under `backend/src/main/java/com/fullstack/platform/`, the original mapper XML, and the original Lua lock/admission examples. Existing attribution comments are retained. Changes include package migration, framework compatibility, configuration replacement, authentication restrictions, cache handling, and the replacement asynchronous-order implementation. The original snapshot remains outside this repository in the local reference materials.

The separately supplied advanced `item-service` example informed the Caffeine cache approach. Its bundled frontend libraries, build output, and complete application were not copied into this repository.

## Design references

The following repositories informed design decisions; no complete module or bundled frontend was copied from them in this baseline:

- https://github.com/java-up-up/hmdp-plus
- https://github.com/yachtywen/ShopLink
- https://github.com/jiao329/City-Review-Project
- https://github.com/youlaitech/vue3-element-admin

Dependencies declared in Maven and npm retain their respective licenses. Consult their published license files when redistributing dependency artifacts.
