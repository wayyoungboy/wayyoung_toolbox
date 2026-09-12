# JavSP-Go 配置说明

**更新日期：2026-07-12**

默认配置是项目根目录的 [`../config.yml`](../config.yml)。该文件的目录是资源根目录：程序会从其中读取 `data/`、`image/`、Pigo 模型和配置中引用的相对资源。复制或发布项目时，请把这些目录与配置文件一同保留。

## 配置文件与优先级

`-c` 或 `-config` 给出的文件总是被选中。未指定时依次寻找：

1. 当前工作目录的 `config.yml`；
2. 可执行文件所在目录的 `config.yml`；
3. 可执行文件所在目录的父目录（支持开发时的 `bin/javsp`）。

选中配置后，键值优先级从高到低为：**重复的 `-o key=value` > `JAVSP_...` 环境变量 > YAML**。`-input` 单独覆盖 `scanner.input_directory`，而不是 YAML 通用覆盖机制的一部分。

```bash
# YAML 先设置 network.retry: 3
JAVSP_NETWORK_RETRY=4 ./bin/javsp -o 'network.retry=5' -input /media/inbox
# 实际值为 5
```

`-o` 的值按 YAML 标量或集合解析，因而布尔值、`null`、数字和列表可直接写入：

```bash
./bin/javsp -o 'scanner.manual=false' \
  -o 'crawler.selection.normal=[javbus,javdb]' \
  -o 'network.proxy_server=null' \
  -input /media/inbox
```

单下划线**不是**把任意点路径转换为环境变量的通用规则。它只支持加载器 `envAliases()` 中的固定键白名单（例如 `JAVSP_NETWORK_TIMEOUT=PT2M`），以及下列动态前缀：`JAVSP_NETWORK_PROXY_FREE_`、`JAVSP_TRANSLATOR_ENGINE_`、`JAVSP_SUMMARIZER_COVER_CROP_ENGINE_`。未列入白名单的单下划线路径会被忽略。

任意嵌套路径（包括 `summarizer.default.*`）请用双下划线逐段分隔；动态映射可使用下列任一种受支持形式：

```bash
# network.proxy_free 的动态站点键
JAVSP_NETWORK_PROXY_FREE_JAVBUS=https://example.invalid
# 双下划线逐段表示嵌套路径，适合任意键和映射键
JAVSP_NETWORK__PROXY_FREE__JAVBUS=https://example.invalid
# engine 是映射；这两个前缀也有简写支持
JAVSP_TRANSLATOR_ENGINE_NAME=google
JAVSP_SUMMARIZER_COVER_CROP_ENGINE_NAME=pigo
# 不在单下划线白名单中的固定键也必须使用双下划线
JAVSP_SUMMARIZER__DEFAULT__TITLE="'#自定义标题'"
```

命令行可用相同的点路径：`-o 'network.proxy_free.javbus=https://example.invalid'`。映射键应使用小写的爬虫 ID。

## 值的格式

### 时长

`network.timeout`、`crawler.sleep_after_scraping` 和 `summarizer.extra_fanarts.scrap_interval` 接受 ISO 8601 时长或 Go 时长。

| 格式 | 示例 |
| --- | --- |
| ISO 8601 周/日/时间 | `P2W`、`P1DT2H`、`PT1H2M3.5S`、`PT0S` |
| Go 时长 | `10s`、`1m30s`、`2h` |

ISO 8601 不支持月和年；使用大写 `P` 与 `T`。`PT0S` 或 `0s` 可表示不等待。

### 字节大小

`scanner.minimum_size` 支持纯字节数，以及 `B`、`KB`、`MB`、`GB`、`TB`（十进制）和 `KiB`、`MiB`、`GiB`、`TiB`（1024 进位）。独立项目默认值为 `1GiB`：严格小于 1GiB 的视频不处理，正好 1GiB 及以上参与扫描。例如也可配置为 `500 MB` 或 `1048576`。

小于该阈值的文件会被直接跳过，即使它与主文件番号相同、看起来像连续分片，也不会进入番号识别、分片合并或后续整理。

## `scanner`

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `scanner.ignored_id_pattern` | 字符串列表 | 识别番号前忽略的正则表达式。 |
| `scanner.input_directory` | 字符串或 `null` | 默认扫描目录；`null` 时根据交互设置询问。 |
| `scanner.filename_extensions` | 字符串列表 | 被视作影片的扩展名。 |
| `scanner.ignored_folder_name_pattern` | 字符串列表 | 扫描时忽略的目录名正则表达式。 |
| `scanner.minimum_size` | 字节大小 | 参与番号识别的最小文件大小。 |
| `scanner.skip_nfo_dir` | 布尔值 | 跳过已有 NFO 的目录；默认 `false`。 |
| `scanner.manual` | 布尔值 | 整理前允许人工确认或更正番号；默认 `false`，启用时需要 `other.interactive: true`。 |

## `network`

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `network.proxy_server` | URL 或 `null` | HTTP、HTTPS、`socks5://` 或 `socks5h://` 代理；默认 `http://127.0.0.1:7897`，`null` 禁用代理。 |
| `network.proxy_free` | `站点 ID → URL` 映射 | 对站点使用的免代理入口。 |
| `network.retry` | 非负整数 | 每个爬虫的重试次数。 |
| `network.timeout` | 时长 | 单次网络请求超时；整体抓取超时按 `retry × timeout` 计算。 |

SOCKS5 例子：

```yaml
network:
  proxy_server: http://127.0.0.1:7897
```

当设置全局代理时，JavBus 和 JavDB 不再使用 `proxy_free` 的临时入口。`proxy_free` 中的 URL 必须是带 scheme 的绝对 URL。

## `crawler`

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `crawler.selection.normal` | ID 列表 | 普通番号的站点顺序。 |
| `crawler.selection.fc2` | ID 列表 | FC2 的站点顺序。 |
| `crawler.selection.cid` | ID 列表 | CID 的站点顺序。 |
| `crawler.selection.getchu` | ID 列表 | Getchu 的站点顺序。 |
| `crawler.selection.gyutto` | ID 列表 | Gyutto 的站点顺序。 |
| `crawler.required_keys` | 字段列表 | 只有这些字段都存在时才视为抓取成功。 |
| `crawler.movie_concurrency` | 正整数 | 同时处理的影片数量；随附配置默认 `3`，旧配置缺省时兼容为 `1`。 |
| `crawler.hardworking` | 布尔值 | 允许部分站点进行更完整但更慢的抓取。 |
| `crawler.respect_site_avid` | 布尔值 | 用站点返回的番号规范化最终番号。 |
| `crawler.fc2fan_local_path` | 路径或 `null` | 本地 fc2fan 镜像目录。 |
| `crawler.sleep_after_scraping` | 时长 | 并发影片任务之间的全局抓取启动间隔。 |
| `crawler.use_javdb_cover` | `fallback`、`yes` 或 `no` | JavDB 封面的选择策略。 |
| `crawler.normalize_actress_name` | 布尔值 | 使用 `data/actress_alias.json` 统一女优别名。 |

全部 20 个有效爬虫 ID 为：`airav`、`avsox`、`avwiki`、`dl_getchu`、`fanza`、`faleno`、`fc2`、`fc2fan`、`fc2ppvdb`、`gyutto`、`jav321`、`javbus`、`javdb`、`javlib`、`javmenu`、`mgstage`、`njav`、`prestige`、`arzon`、`arzon_iv`。

`required_keys` 可用字段：`dvdid`、`cid`、`url`、`plot`、`cover`、`big_cover`、`genre`、`genre_id`、`genre_norm`、`score`、`title`、`ori_title`、`magnet`、`serial`、`actress`、`actress_pics`、`director`、`duration`、`producer`、`publisher`、`uncensored`、`publish_date`、`preview_pics`、`preview_video`。

## `summarizer`

### 总开关、路径与标题

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `summarizer.move_files` | 布尔值 | `true` 移动媒体至输出目录；`false` 时只在原目录写资料文件。 |
| `summarizer.path.output_folder_pattern` | 模板 | 输出目录模板；相对路径以本次扫描目录为基准并按系统路径规则规范化，因此默认写入扫描目录下的 `#整理完成`，且兼容原项目的 `../` 片段。绝对路径保持不变。 |
| `summarizer.path.basename_pattern` | 模板 | 影片、封面等的基础文件名模板。 |
| `summarizer.path.length_maximum` | 正整数 | 路径最长长度；超出时截短标题。 |
| `summarizer.path.length_by_byte` | 布尔值 | 以字节而非字符计算路径长度。 |
| `summarizer.path.max_actress_count` | 正整数 | 模板中女优列表的最大人数。 |
| `summarizer.path.hard_link` | 布尔值 | 移动时优先创建硬链接以节省空间。 |
| `summarizer.title.remove_trailing_actor_name` | 布尔值 | 删除标题末尾疑似女优姓名。 |

### 缺省值与 NFO

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `summarizer.default.title` | 字符串 | 标题缺失时的值。 |
| `summarizer.default.actress` | 字符串 | 女优缺失时的值。 |
| `summarizer.default.series` | 字符串 | 系列缺失时的值。 |
| `summarizer.default.director` | 字符串 | 导演缺失时的值。 |
| `summarizer.default.producer` | 字符串 | 制作商缺失时的值。 |
| `summarizer.default.publisher` | 字符串 | 发行商缺失时的值。 |
| `summarizer.nfo.basename_pattern` | 模板 | NFO 基础文件名。 |
| `summarizer.nfo.title_pattern` | 模板 | NFO 中显示的标题。 |
| `summarizer.nfo.custom_genres_fields` | 模板列表 | 写入自定义分类的字段；空列表为不写。 |
| `summarizer.nfo.custom_tags_fields` | 模板列表 | 写入自定义标签的字段；空列表为不写。 |
| `summarizer.censor_options_representation` | 三项字符串列表 | 依次表示无码、有码和未知的 `{censor}` 文本。 |

### 封面、海报与剧照

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `summarizer.cover.basename_pattern` | 模板 | 海报文件基础名。 |
| `summarizer.cover.highres` | 布尔值 | 优先使用高分辨率封面候选。 |
| `summarizer.cover.add_label` | 布尔值 | 为字幕或无码海报叠加本地标记。 |
| `summarizer.cover.crop.on_id_pattern` | 正则列表 | 对匹配标签启用人脸优先裁剪。 |
| `summarizer.cover.crop.engine.name` | `pigo` 或旧别名 | `pigo` 使用本地 Pigo 检测器；`slimeface` 仅是同一检测器的旧配置别名。 |
| `summarizer.fanart.basename_pattern` | 模板 | 横版封面文件基础名。 |
| `summarizer.extra_fanarts.enabled` | 布尔值 | 下载额外剧照。 |
| `summarizer.extra_fanarts.scrap_interval` | 时长 | 额外剧照请求之间的间隔。 |

将 `engine.name` 设为 `pigo` 后，程序从**选中配置文件的目录**读取 `data/facefinder`。对于无码、FC2 或匹配 `on_id_pattern` 的标签，Pigo 会优先选择检测评分最高的人脸；没有正向检测结果时改用确定性的默认裁剪。缺失或无效模型会报出该模型的解析后绝对路径。设置 `add_label: true` 时，程序从同一资源根读取 `image/sub_mark.png` 和 `image/unc_mark.png`。

## `translator`

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `translator.engine` | 映射或 `null` | 默认使用 Google；`null` 或 `name: none` 禁用翻译。 |
| `translator.engine.name` | 字符串 | `none`、`google`、`bing`、`baidu`、`claude` 或 `openai`。 |
| `translator.engine.api_key` | 字符串 | Bing、Baidu、Claude 和 OpenAI 所需的密钥。 |
| `translator.engine.app_id` | 字符串 | Baidu 的应用 ID。 |
| `translator.engine.url` | URL | OpenAI 兼容接口地址。 |
| `translator.engine.model` | 字符串 | OpenAI 兼容接口的模型名。 |
| `translator.fields.title` | 布尔值 | 翻译标题。 |
| `translator.fields.plot` | 布尔值 | 翻译剧情简介。 |

全部翻译引擎的配置例子：

```yaml
# 不翻译
translator: {engine: null, fields: {title: true, plot: true}}

# Google
translator: {engine: {name: google}, fields: {title: true, plot: true}}

# Bing
translator: {engine: {name: bing, api_key: '...'}, fields: {title: true, plot: true}}

# Baidu
translator: {engine: {name: baidu, app_id: '...', api_key: '...'}, fields: {title: true, plot: true}}

# Claude
translator: {engine: {name: claude, api_key: '...'}, fields: {title: true, plot: true}}

# OpenAI 兼容接口
translator: {engine: {name: openai, url: 'https://example.invalid/v1/chat/completions', api_key: '...', model: 'model-name'}, fields: {title: true, plot: true}}
```

## `other`

| 键 | 类型 | 作用 |
| --- | --- | --- |
| `other.interactive` | 布尔值 | 允许终端输入和确认。 |
| `other.check_update` | 布尔值 | 为已发布版本发起只读更新检查；默认 `false`。 |
| `other.auto_update` | 布尔值 | 配置兼容键；请保持 `false`。 |

若 `scanner.manual: true` 而 `other.interactive: false`，程序会拒绝运行，因为无法进行人工确认。
