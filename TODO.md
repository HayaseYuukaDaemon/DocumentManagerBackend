# TODO

## 近期计划

- 梳理 `Progress.Done` / `Progress.Total` 的所有权。
  - 当前 `Done` 逐步改为由 store 的 `AddPage` / `RemovePage` 维护，但 `UpdateMeta` 仍可修改完整 `Progress`。
  - 后续考虑收窄 meta 更新能力，或者提供更明确的 progress reset/update API。
