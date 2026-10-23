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
| XZ 解压 | `github.com/ulikunitz/xz` v0.5.17 | BSD-3-Clause |
| LZ4 解压 | `github.com/pierrec/lz4/v4` v4.1.33 | BSD-3-Clause |
| Zstd 解压 | `github.com/klauspost/compress` v1.20.3 | BSD-3-Clause；完整文件声明另含 Apache/MIT 条款 |

Go 和 npm 依赖归其各自作者所有，并继续适用上游许可证。生成的 FPK 会在 `app/licenses` 中附带本项目、模块及其已收集依赖的许可证和声明。

兼容播放的 Go 会话管理为本项目独立实现，调用系统已有 FFmpeg/FFprobe，不包含或重新分发 FFmpeg 二进制。使用 FFmpeg 的设备须遵循其实际构建对应的许可证。HLS.js 的许可证随 FPK 一并保留；未复制或跨语言翻译 Peach 的转码源码。

本地偏好分析、规范演员关注与来源开关使用本仓库的 SQLite 数据模型和规则计算实现，没有新增推荐服务或复制/逐行翻译上游偏好代码。行为对照参考 [Peach 固定基线](https://github.com/peach-mitao/peach/tree/de22796c04919aaa057d77930c33c81313765569)；Peach 本身适用 AGPL-3.0-or-later，本项目后续若直接复用或移植其代码，须另行保留归属并遵循相应许可，不能因改用 Go 而省略。

浏览器文件导入按上述 Peach 基线、[browserexport 0.4.4 数据格式](https://github.com/purarue/browserexport/tree/v0.4.4) 和浏览器公开数据结构核对行为后，使用 Go、现有 SQLite / x/net/html 和表中解压库独立实现；没有复制 Python 适配器源码。所有样本独立生成，不包含用户浏览器资料。新增解压库的完整原版 LICENSE 保存在 `modules/media/licenses/browser-decoders`，构建时原样放入 FPK 的 `app/licenses/browser-decoders`，包括 compress 上游附带的按文件声明。
