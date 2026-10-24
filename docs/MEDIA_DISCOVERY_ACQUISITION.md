# 发现、想要与获取：后续完整范围

这是完整目标的行为核对和实施边界，不是已交付功能列表。当前交付状态仍以 [功能对照](MEDIA_CENTER_PARITY.md) 为准；已有[资源搜索](RESOURCE_SEARCH.md)及[显式下载](RESOURCE_DOWNLOADS.md)，0.7.10 源码新增[想要与实际入库关联](WANTS.md)，没有用手工清单或原有 RSS 替代完整发现。

基线为 Peach 固定提交 `de22796c04919aaa057d77930c33c81313765569`。仅以行为契约独立设计 Go 模型，不直接逐行翻译 Python 实现；若后续复用实现，须另外记录 AGPL 归属及义务。

## 不可合并的事实

| 事实 | 含义与边界 |
| --- | --- |
| 本地关注 | 本应用人物身份与偏好信号，不表示已绑定站外演员身份 |
| 订阅新作 | 明确选择 JavDB 演员身份后，只发现作品；不等于当前会自动投递下载的 RSS 规则 |
| 想要 | 可以尚无馆藏资产；不同于“稍后观看” |
| 搜索候选 | 只读来源及缓存，不自动选中或下载，不累计放弃次数 |
| 提交与远端进度 | 有独立 infohash 幂等、追加审计与对账事实；不确定提交不能盲目重发 |
| 落地与入库 | 必须确认受限挂载路径中的实际主视频，再登记到同一馆藏；远端完成不是已入库 |
| 后处理 | 独立于原始下载；失败只重试后处理，不能重新提交资源 |

## F12 发现与想要

- 新作订阅实际入口是人物“订阅新作”和演员身份查找/确认；不接受前端任意 URL。启用、暂停、移除、立即拉取、到期持久任务及失败退避要分别实现。删除来源不删除已经发现的作品。
- 来源条目按 `(source_id,item_key)` 去重，作品按规范番号全局唯一。已读与忽略独立；加入想要清除忽略，忽略移除非已入库想要，取消忽略不自动恢复想要。
- 想要持久状态只有 `wanted / given_up / acquired`；`unreleased` 按发行日现算。保留来源、标题、发行日、演员、厂牌和关联快照；补充资料不覆盖人工填写的非空值。
- 入库状态依据实际馆藏资产，不凭下载状态推断；登记要与生产者任务一起原子提交，并保留关联凭据以支持复核。
- 列表 GET 只读缓存。该固定基线的 `record_search / pending_targets` 没有生产调用；不能因存在“三次空结果”的模型测试，就宣称上游已有自动搜索下载循环，或在刷新时累计放弃次数。

依据：[wants.py](https://github.com/peach-mitao/peach/blob/de22796c04919aaa057d77930c33c81313765569/src/peach/wants.py)、[web_wants.py](https://github.com/peach-mitao/peach/blob/de22796c04919aaa057d77930c33c81313765569/src/peach/web_wants.py)、[JAV 导航](https://github.com/peach-mitao/peach/blob/de22796c04919aaa057d77930c33c81313765569/frontend/src/react/jav/jav-navigation.ts)。

## F16 资源与云下载

- 基线实际自动搜索为 manko.fun、JavDB、sukebei，另有用户配置的 Torznab 后备。声明的 JavBus/16mag/OneJAV/Free JAV Torrent 是后续来源，不能把它们都写成已经实现的爬虫。参考站点与自动解析来源分别展示。
- 搜索保留候选证据、warning、trace、后续来源与参考来源。按规范番号匹配，按 BTIH 跨来源合并；未知大小、画质、中文字幕、无码或水印不能冒充已验证。失败保留成功缓存并显示 stale。
- Torznab 后续须有来源数量、45 秒、2 MiB XML、候选/抓种子数量预算；种子抓取只限索引器同源，不接受任意重定向或外部实体。
- 独立下载生产者不能伪造 `RSSID=0` 塞进现有要求真实订阅的执行器。应抽取公共的预占/提交审计/不确定结果对账机制，保留原 RSS 去重语义和 qB hash+AddedOn 身份。
- 下载至少区分提交、远端运行、远端完成、实际落地、馆藏入库，以及失败/取消/停滞。取消进行中的远端任务需要远端确认；完成后停止本地跟踪不自动删除文件。附件不能作为主视频。
- 115/CloudDrive2 的 gRPC、PikPak 的原生 HTTP/refresh token/用户登录与验证码是独立真实渠道，不是 qB 的别名或空适配器。凭据留空保留、显式 clear 才清除；不进入 ledger、URL、日志或 API 回显。

依据：[magnet_chain.py](https://github.com/peach-mitao/peach/blob/de22796c04919aaa057d77930c33c81313765569/src/peach/magnet_chain.py)、[资源 API](https://github.com/peach-mitao/peach/blob/de22796c04919aaa057d77930c33c81313765569/src/peach/web_resource_search.py)、[downloads.py](https://github.com/peach-mitao/peach/blob/de22796c04919aaa057d77930c33c81313765569/src/peach/downloads.py)。

后续仍需要身份绑定的新作订阅、feed 已读/忽略、来源补全、多来源与 Torznab、远端取消，以及各真实云渠道的配置/认证/轮询/落地验收。手工想要状态、入库凭据和独立显式下载生产者已实现的部分分别记录，不据此标记完整 F12/F16 完成。
