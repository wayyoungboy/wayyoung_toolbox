# 第三方项目与许可证

本文件区分“实际依赖”和“设计/实现参考”。只有实际依赖会随本项目编译进入程序。

## 实际依赖

### ncmdump-go

- 角色：NCM 本地文件解析、内嵌音频提取与元数据写入
- 版本：`v1.7.5`
- 提交：`a62f5156f0cc2f87661bf9e06f6ca54a004f6276`
- 仓库：https://git.taurusxin.com/taurusxin/ncmdump-go
- 许可证：MIT
- 许可证全文：[licenses/ncmdump-go-MIT.txt](licenses/ncmdump-go-MIT.txt)

本项目使用 `third_party/ncmdump-go` 中保存的对应上游源码，并通过 Go module `replace` 固定构建输入。相对上游的本地兼容补丁识别 ID3、MPEG 音频帧、标准 `fLaC` 及 `ftyp` / `M4A ` 音频容器，避免将非 ID3 音频全部错判为 FLAC。补丁带有格式识别、字节保持和标签写入边界的回归测试，不修改 NCM 解密算法。M4A 按原样还原，保留已有容器标签，但不调用上游仅支持 MP3 / FLAC 的标签写入器，不补写 NCM 标题和封面。服务端在其外层增加了路径限制、异常隔离、冲突策略、并发控制与任务记录。

## 设计与实现参考

以下项目用于理解已有交互、批处理或元数据处理方案；本项目没有直接复制它们的源码，也不把它们声明为运行时依赖。

| 项目 | 参考内容 | 仓库 | 许可证 |
| --- | --- | --- | --- |
| ncmdump-gui | 批量任务和并行处理的界面思路 | https://git.taurusxin.com/taurusxin/ncmdump-gui | MIT |
| ncmdump | NCM 工具的成熟命令行实现与行为边界 | https://github.com/taurusxin/ncmdump | MIT |
| ncmdump-py | Python 生态中的元数据与封面处理思路 | https://github.com/ww-rm/ncmdump-py | MIT |

此外，`ncmdump-go` 自身说明其为 `ncmdump` 的 Go 移植版本；这是上游项目之间的来源关系，并不表示本项目直接包含 `ncmdump` 的 C++ 源码。

## 商标与隶属关系

网易云音乐及相关名称、商标归其权利人所有。本项目是独立的第三方学习项目，与网易云音乐、飞牛 fnOS 及上述第三方项目均无官方隶属、赞助或背书关系。
