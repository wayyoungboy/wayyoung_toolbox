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
| HLS.js 1.7.3（兼容播放器，按需加载 light 构建） | npm `hls.js`，上游 https://github.com/video-dev/hls.js | Apache-2.0 |

Go 和 npm 依赖归其各自作者所有，并继续适用上游许可证。生成的 FPK 会在 `app/licenses` 中附带本项目、模块及其已收集依赖的许可证和声明。

兼容播放的 Go 会话管理为本项目独立实现，调用系统已有 FFmpeg/FFprobe，不包含或重新分发 FFmpeg 二进制。使用 FFmpeg 的设备须遵循其实际构建对应的许可证。HLS.js 的许可证随 FPK 一并保留；未复制或跨语言翻译 Peach 的转码源码。
