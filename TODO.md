# TODO

## 近期目标

### 实现nhentai handler

### 实现细粒度权限控制

## 优化目标

### 小目标
- 实现灾难恢复功能
  - 比如程序启动时, 在当前目录下建立一个`.dmb-running`的stub文件, 正常退出时由程序删除
  - 如果异常退出, 例如收到`SIGKILL`, 该文件不会被移除, 下次程序启动时将检测到此标记.
  - 此时程序需要进入不一致性恢复状态, 具体如何恢复, 以后再探讨吧

- 梳理 `Progress.Done` / `Progress.Total` 的所有权。
  - 当前 `Done` 由 store 的 `AddPage` / `RemovePage` / `ResetPages` / `Purge` 维护，但 `UpdateMeta` 仍可修改完整 `Progress`。
  - 后续考虑收窄 meta 更新能力，或者提供更明确的 progress reset/update API。

- 实现数据库版本管理与迁移
  - 在公司干活发现公司有个能够自动迁移数据库的功能, 只要写好了`*.up.sql`这种schema, 程序能自动根据schema升级数据库.
  - 这个需要改表结构, 好像要加个`versions`表

- 把`IndexedError`用起来, 有批量处理时报错的地方都写上

- 再开个表, 表名叫`maintainance_log`, 专门记录定期删除等维护事件

### 大目标
