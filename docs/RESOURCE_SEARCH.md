# 按番号搜索资源（0.7.8 测试版）

入口为媒体中心“发现与采集 → 资源搜索”，也可以从馆藏作品详情展开“搜索此作品的资源”。RSS 订阅与资源查询共享采集工作区，原来的订阅、队列及设置链接继续可用；手机主导航保持八项、两行。

工具箱 0.7.8 / 媒体组件 0.1.8 已完成 x86_64 NAS 安装和数据保留核对。单作品真实 JavDB 查询返回 11 条候选并正确落入缓存，没有新增下载、整理或扫描任务。这只证明验收时该站点与配置可用，不保证所有作品、网络或未来页面都可解析。

## 使用与状态

1. 输入完整、已确认的番号。小写会规范化为大写；不从文件名、标题或 URL 提取子串，不猜测 `_646` 之类缺失的前缀。
2. “读取上次结果”只读 SQLite 缓存，不请求外站。“搜索 JavDB”才会发起查询；打开页面、展开详情、切换页签和刷新不会自动查询。
3. 仅使用 JAVSP 已为对应类型启用的 JavDB，以及已有站点入口与代理配置。不访问其他爬虫、不探测替代镜像、不读取浏览器 Cookie。代理未配置或为空时直连，不继承环境代理。
4. 分别显示未查询、成功候选、详情未提供磁力、无匹配作品、登录/权限受限、站点拦截、身份不一致、超时、不可解析和组件配置不可用。不能把失败解释成“没有资源”。
5. 查询失败保留上次成功结果与成功时间，同时保存失败状态和尝试时间；刷新后仍显示旧缓存警告。真正读到空结果才替换旧候选。缓存最多 200 个“番号 + 来源”，事务内轮转；服务重启保留。
6. 支持复制规范磁力或主动打开来源页。没有加入下载、自动投递、扫描、整理、改名或媒体文件写入动作。取消只停止当前查询/等待，不取消原有采集任务；请求结束前再次搜索可能收到“另一个资源查询正在运行”。

目前只接入 JavDB。候选只是网站提供的资源，并未验证实际内容、大小、做种、编码、字幕、无码、水印或画质；这些信息不会虚构为已确认。磁力只保留规范 BTIH，去掉 tracker/passkey 等参数，因此不适合依赖私有 tracker 的下载。不要把这一切片标成完整 Peach F16，更不是想要、新作订阅或云下载已经完成。

## 安全与预算

- 复用既有 Go JAVSP 解析器，但独立于 `engine.Run`，不调用 scanner / organizer / translator。搜索详情必须确认对应番号；不使用仅有元数据的搜索卡片回退来证明资源为空。
- API 仅限既有 fnOS 管理员入口；POST 要求 JSON、同源和过程级 CSRF。客户端不能提交站点 URL、任意来源或下载参数。GET 拒绝额外、重复及无效编码的查询参数。
- 每进程同时一个资源查询，最长 45 秒；每个响应在 HTML 与 403 解析之前限制 2 MiB，最多六次同源请求、有限重定向。拒绝跨 origin 的详情链接与重定向；请求 URL 最多 2048 字节，配置/输出来源 URL 最多 1024 字节，最多 50 个按 20 字节 BTIH 去重的候选。
- 站点地址是管理员已有配置的信任边界，可包括明确配置的自托管地址；该接口不是接受任意用户 URL 的抓取代理。保留标准证书校验，不绕过 TLS、Cloudflare 或登录限制。
- 不回显原始代理、Cookie、请求异常或带查询参数的来源 URL。缓存只接受有界规范结果；错误响应不把原始抓取页面写入日志或 API。
- 新增一张 `resource_search_cache` 表；没有新常驻服务、数据库 ATTACH、爬虫删除或原任务状态迁移。

## API

```text
GET  /api/resources/search?code=TEST-001&provider=javdb  # 本地缓存
POST /api/resources/search                            # 显式网络查询
     {"code":"TEST-001","provider":"javdb"}
```

结果包含 `code / provider / state / items / searched_at / attempted_at / cached / stale`。候选包含 `infohash / magnet / name / source / source_url / verification`；当前 `verification` 固定为 `unverified`，不伪装成已验真。站点结果状态可通过 HTTP 200 返回，不表示已下载。无效请求 400、权限/CSRF 403、并发 409、请求取消 408、组件配置不可用 503；不会自动重试 POST。

## 测试

`engine/resources_test.go`、`service/resource_search_test.go` 和 `resource-browser.mjs` 使用本地 HTTP 站点、中性作品和实际 SQLite。覆盖精确身份、卡片回退隔离、权限/403/空结果、跨源/体积/URL/候选预算、BTIH 大小写与 Base32 去重、私有参数移除、类型开关、空代理、CSRF、只读 GET、取消/并发、缓存轮转/回滚/重启，以及桌面/手机/深色和旧番号结果隔离。没有用真实影片或下载任务作测试。

本地资源与原馆藏全旅程通过：包含实际 HTTP/SQLite、合成 H.264/AAC 播放与字幕、评分/历史/列表、删除隔离样本及任务归档，没有未解释的控制台异常。Browser 插件未提供，使用现有 Playwright；视口 1440×1000、390×844。Go/race/vet、179 项全仓库前端测试及构建通过；资源 API 核心文件此前专项语句覆盖率 84.4%，资源 engine 核心 85.8%，不是全产品覆盖率。

NAS 运行资源 API 隔离回归通过，覆盖当前 JavDB 简化 class 不误报空缓存；浏览器核对新入口、只读缓存、零自动 POST 以及桌面/手机/深色布局。原馆藏全旅程使用新建的中性样本重新通过，包含 Linux 实际软件 HLS 的暂停、跳转、续播和回收。首轮头像上传因本地缺少样本失败，补齐验收文件后从全新样本重跑成功；没有改写生产影片。

真实站点验收先发现旧解析器将页面上的 11 个锚点误报为零。按 TDD 先执行失败用例，修复响应式 class 的精确字符串依赖；再补同名无关组件的失败用例，把查询限定在作品详情及明确的相邻资源面板。类名顺序变化、精确 class token 和无关 aside 均有回归。最终安装后的单作品探测为 `ready`、11 条候选，GET 缓存仍为 `ready`，未触发媒体任务。保留标准 TLS 校验，不使用 Cookie、镜像探测或验证绕过；本次没有重新验收其他爬虫。

fnOS 1.2.0800（Mainland-PE，x86_64）、内核 6.18.18.c1126-trim 原生 fnpack 构建 FPK。安装前确认模块空闲，按用户选择不新增备份；初装时 30 张既有表完全一致、新缓存表为空，修复包复装时全部 31 张表及模块开关/JAVSP 配置摘要完全一致，数据库完整性 `ok`、外键错误零。已安装程序和 52 个前端文件与打包目录一致，五个原启用模块正常，自动登记仍关闭。ARM64 只交叉编译通过，未构建 ARM FPK 或作 ARM 实机验收。完整记录见[阶段说明](MEDIA_CENTER_STATUS.md)。

缓存表将在本版首次交付。TDD 历史中的四列中间 schema 只存在于本地临时测试库，从未作为应用包安装或用于用户持久数据；实际 0.7.7 升级只会新建最终六列表，不发布或安装中间检查点。

```bash
cd modules/media/third_party/javsp
go test -race ./engine ./internal/crawler/sites -run 'TestResourceSearch|TestJavDB' -count=1
cd ../..
go test -race ./internal/service -run '^TestResourceSearch' -count=1
npm run build:frontend
CATALOG_PLAYWRIGHT=/absolute/path/to/playwright CATALOG_REPORT_DIR=/tmp/resource-qa \
  go test -tags=catalog_browser ./internal/service -run '^TestResourceBrowserJourney$' -count=1 -v
```
