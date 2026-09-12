# Third-party notices

wayyoung 的飞牛工具箱将多个模块编译到同一个程序中。下表列出仓库内直接包含的模块及其许可证；每个目录中的 `LICENSE` 和 `THIRD_PARTY_NOTICES.md` 保留完整条款与依赖声明。

| 组件 | 源码目录 | 许可证 |
| --- | --- | --- |
| 飞牛备份中心 | `modules/backup` | MIT |
| 影片采集任务中心 | `modules/media` | GPL-3.0 |
| JAVSP Go | `modules/media/third_party/javsp` | GPL-3.0 |
| 音乐转换中心 | `modules/music` | MIT |
| ncmdump-go | `modules/music/third_party/ncmdump-go` | MIT |
| HA500 硬件工具 | `modules/ha500` | MIT |

Go 和 npm 依赖归其各自作者所有，并继续适用上游许可证。生成的 FPK 会在 `app/licenses` 中附带本项目、模块及其已收集依赖的许可证和声明。
