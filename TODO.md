# TODO

## 近期计划

- 围绕“保留现有页面行”重新设计页面更新语义。
  - 不要仅为了这个目标就在通用 document model/store 中持久化或维护每个 document page 的 hash。
  - page 的 “hash” 应理解为来源侧的页面变更标识，不一定是狭义的加密哈希；只要能针对该 source 判断页面是否变动即可。
  - 在 document update 的 prestage 中记录现有 pages，并与 update 后的 pages 对比，用来决定哪些页面行需要插入/更新、哪些已有页面行应保留。
  - 可以考虑新增显式的 `AddPage`/页面更新方法，避免把增量页面写入都塞进整份 document 的 `Update`。
- 复查 `internal/documents/sqlite_store.go`。
  - 检查事务边界、页面更新语义、时间戳更新，以及重复 helper 逻辑。
  - 确认 document update 不会意外擦掉已有页面行。
- 梳理 `Progress.Done` / `Progress.Total` 的所有权。
  - 当前 `Done` 逐步改为由 store 的 `AddPage` / `RemovePage` 维护，但 `UpdateMeta` 仍可修改完整 `Progress`。
  - 后续考虑收窄 meta 更新能力，或者提供更明确的 progress reset/update API。
- 实现 `S3Storage`。
  - 编码前先对比当前 `ObjectStore` 接口与 S3 语义是否匹配。
  - 增加 endpoint、bucket、region、credentials，以及 path-style/virtual-host style 行为的配置。
