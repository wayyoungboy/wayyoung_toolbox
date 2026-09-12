![JavSP-Go](image/JavSP.svg)

# JavSP-Go

**更新日期：2026-07-12**

JavSP-Go 是一个可独立复制、构建和运行的 AV 元数据刮削与整理工具。它从影片文件名识别番号，按配置的站点顺序抓取并合并资料，生成供 Emby、Jellyfin、Kodi 等媒体库使用的 NFO、封面和剧照文件。

运行时只需要本项目目录中的可执行文件、`config.yml`、`data/` 和 `image/`；**不需要 Python 运行时**。项目使用 Go 1.22，封面的人脸优先裁剪由内置的 Pigo 检测器完成。

## 功能

- 识别普通番号、FC2、CID、Getchu 和 Gyutto 来源，并处理分片影片。
- 默认只扫描 1 GiB 及以上的视频文件；严格小于 1 GiB 的文件不处理。
- 按优先级并行抓取并合并 20 个已注册站点的数据。
- 生成 NFO，统一类型映射与女优别名，并按模板命名、移动或硬链接文件。
- 下载并校验封面、横版封面和额外剧照；保存前会规范化图片格式和方向。
- 以 Pigo 的本地 `data/facefinder` 模型对指定系列、FC2 和无码影片进行人脸优先海报裁剪；未检测到人脸时使用确定性的右侧裁剪。
- 可叠加 `image/sub_mark.png` 与 `image/unc_mark.png` 标签。
- 可选 Google、Bing、Baidu、Claude 或 OpenAI 兼容接口翻译标题与简介。
- 仅在显式启用时检查新版本；不会自动下载或安装更新。

## 支持平台

开发构建可在 Go 1.22 支持的系统运行。独立发布设计覆盖下列六个目标：

- macOS：amd64、arm64
- Linux：amd64、arm64
- Windows：amd64、arm64

发布包采用无 CGO 构建。当前构建与发布设计见 [构建说明](docs/BUILD.md)。

## 快速开始

准备 Go 1.22 后，在本目录执行：

```bash
go build -o bin/javsp ./cmd/javsp
./bin/javsp -input "/绝对路径/待整理影片"
```

默认会优先在当前目录查找 `config.yml`。也可以直接指定项目内的配置文件：

```bash
./bin/javsp -config ./config.yml -input "/绝对路径/待整理影片"
```

配置文件所在目录就是资源根目录，因此其同级必须保留 `data/` 与 `image/`。详细的查找顺序、每个键和覆盖规则见 [配置说明](docs/CONFIGURATION.md)。

## 命令行参数

| 参数 | 说明 |
| --- | --- |
| `-c PATH`、`-config PATH` | 选择配置文件；显式路径具有最高配置文件选择优先级。 |
| `-input DIR` | 指定扫描目录，覆盖 `scanner.input_directory`。 |
| `-o key=value` | 覆盖一个配置值；可重复使用，优先级高于环境变量和 YAML。 |
| `-version` | 显示版本、提交和构建日期后退出。 |

默认不会在整理前等待人工确认。随附配置默认启用 Google 翻译，并通过 `http://127.0.0.1:7897` 代理访问网络：

```bash
./bin/javsp -config ./config.yml \
  -input "/绝对路径/待整理影片"
```

## 生成的文件

输出目录、基础文件名和是否移动影片由 `summarizer` 配置决定。一次成功处理通常会产生：

默认配置会把影片整理到本次扫描目录下的 `#整理完成/`。相对的 `summarizer.path.output_folder_pattern` 先以扫描目录为基准再按系统路径规则规范化，保持原 JavSP 对 `../` 等路径片段的兼容；绝对路径保持不变。

- `movie.nfo`（或 `summarizer.nfo.basename_pattern` 指定的名称）；
- `fanart` 横版封面与 `poster` 海报（实际扩展名与写出的图片格式一致）；
- 可选的额外剧照；
- 按 `summarizer.path` 模板整理后的影片及关联文件。

## 运行时资源与 Pigo

请随项目保留以下文件：`data/actress_alias.json`、四个 `data/genre_*.csv`、`data/facefinder`、`image/JavSP.svg`、`image/sub_mark.png` 和 `image/unc_mark.png`。`facefinder` 是 Pigo 的级联模型；其归属和许可证见 [第三方声明](THIRD_PARTY_NOTICES.md)。`engine.name: slimeface` 仅作为指向同一原生 Pigo 检测器的旧配置别名保留。

## 安全与法律

本工具只应在你有权访问、处理和保存的本地媒体与站点内容上使用。请遵守所在地区的法律、站点条款、版权规则及网络访问限制；不要将其用于商业或侵权用途。网络数据、站点可用性及翻译服务均由相应提供方负责。

## 相关文档

- [配置说明](docs/CONFIGURATION.md)
- [构建与发布说明](docs/BUILD.md)
- [功能对照](docs/PARITY.md)
- [第三方声明](THIRD_PARTY_NOTICES.md)
- [上游项目与问题反馈](https://github.com/Yuukiy/JavSP)
