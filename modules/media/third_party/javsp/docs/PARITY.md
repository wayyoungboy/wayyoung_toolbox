# JavSP-Go 功能对照

**更新日期：2026-07-14**

本表只对照原项目普通入口可到达、且已在 Go 模块实现的工作流；每一项都链接到 Go 包和至少一个具名测试。它不是旧 README 的愿望清单，**不包含旧 README 中的 TODO 项**。

## 主流程

| 原工作流 | Go 实现 | 具名验证 |
| --- | --- | --- |
| 启动、参数解析、加载配置、扫描到产物写入的串联 | `cmd/javsp/main.go` | `TestParseCLIConfigAliases`、`TestProcessMovieWritesNFOAndPoster`、`TestProcessMovieCIDFallsBackToNormalWhenCIDCrawlerHasNoTitle` |
| 显式配置、当前目录/可执行目录的配置发现，以及以配置目录为资源根 | `internal/projectpath`、`internal/config` | `TestResolveConfigExplicitPathWins`、`TestResolveConfigDiscoversDefaultInOrder`、`TestResourceRootReturnsAbsoluteConfigDirectory`、`TestLocalRuntimeAssetsExistRelativeToConfig` |
| YAML、环境变量和重复命令行覆盖 | `internal/config` | `TestShippedConfigLoads`、`TestLoadWithOverridesAppliesEnvironmentAndCLI`、`TestValidateRejectsInvalidConfigValues`、`TestValidateTranslatorEngineRequiresOriginalModelFields` |
| 番号、FC2、CID 识别及来源判断 | `internal/avid` | `TestGetIDNormal`、`TestGetIDFC2`、`TestGetCIDValid`、`TestGuessAVType` |
| 递归扫描、1 GiB 默认硬门槛、分片组合、重复番号选择最大 MP4、同番号预告片隔离、忽略目录与 NFO 目录 | `internal/scanner` | `TestScanSingleMovie`、`TestScanOneGiBThresholdIsInclusive`、`TestScanSlices123`、`TestScanSlicesABC`、`TestInvalidSliceStructureSelectsLargestMP4`、`TestDuplicateIDPrefersMP4BeforeOtherExtensions`、`TestSmallVideoNeverJoinsRecognizedMovie`、`TestSmallNonSliceWithSameIDDoesNotHideMainMovie`、`TestSmallNonSliceDoesNotBreakValidLargeSlices`、`TestIgnoredFolderPatternUsesPythonMatchSemantics` |
| 终端人工确认和手工修正番号 | `cmd/javsp/main.go` | `TestReviewMoviesAllowsManualIDCorrections`、`TestReviewMoviesForConfigRejectsManualNonInteractive`、`TestReviewMoviesRejectsClosedInputBeforeConfirmation` |
| 网络请求、代理、下载、站点封锁识别 | `internal/httpclient` | `TestHTTPClientReturnsConfiguredClient`、`TestGetHTMLSendsUserAgentAndParsesXPath`、`TestDownloadLocalAndRemote`、`TestGetDetectsCloudflareChallenge` |
| 动态免代理入口解析与可连接地址选择 | `internal/proxyfree` | `TestCandidateURLParsers`、`TestChooseConnectable`、`TestParseScriptURLsResolvesRelativeURLs` |
| 注册爬虫、并行执行、重试、零重试、终止错误和总体超时 | `internal/crawler`、`internal/crawler/sites` | `TestRegisterAllRegistersSupportedCrawlers`、`TestRunnerReturnsConfiguredOrder`、`TestRunnerRetriesTransientErrors`、`TestRunnerZeroRetriesPerformsNoParseAttempt`、`TestRunnerDoesNotRetryTerminalErrors`、`TestRunnerOverallTimeoutReturnsOrderedPartialResults` |
| 多站点字段优先级汇总、JavDB 封面策略、类型映射、女优别名与必填字段 | `internal/summary`、`internal/genre` | `TestMergeUsesPriorityAndJavDBGenreAndCoverFallback`、`TestMergeAppliesGenreMap`、`TestLoadMapAndApply`、`TestLoadActressAliases` |
| 翻译标题和简介，以及 Google/Baidu 原版限流行为 | `internal/translate` | `TestApplyTranslatesTitleAndPlot`、`TestNewEngineFromConfig`、`TestGoogleTranslate`、`TestGoogleTranslateRetriesRateLimitLikeOriginal`、`TestBingTranslateProtectsActressNames`、`TestBaiduTranslate`、`TestBaiduTranslateRateLimitWaitHonorsContext`、`TestClaudeTranslate`、`TestOpenAITranslate` |
| 相对扫描根目录的输出模板、最终视频路径长度、文件移动、硬链接与跨文件系统回退 | `cmd/javsp/main.go`、`internal/organizer`、`internal/util` | `TestResolveOutputFolderPatternUsesScanRootForRelativeTemplate`、`TestResolveOutputFolderPatternSupportsRelativeScanRootAndParentSegments`、`TestShippedOutputPatternResolvesToFinishedFolderInsideScanRoot`、`TestGenerateNamesUsesTemplatesAndAttrSuffix`、`TestGenerateNamesTruncatesLongTitleToLengthMaximum`、`TestGenerateNamesChecksVideoBasenameAgainstLengthLimit`、`TestMoveFilesRenamesAndUpdatesNewPaths`、`TestMoveFilesFallsBackToCopyAcrossFilesystems` |
| 写入 NFO 和自定义分类/标签 | `internal/nfo` | `TestWriteNFO`、`TestWriteCustomFieldsUseConfiguredDefaults` |
| 下载封面/高清候选、DMM 回退、空候选失败、候选重试与排序、校验图片、规范化格式和额外剧照 | `cmd/javsp/main.go`、`internal/poster` | `TestDownloadCoverArtifactsFailsWithoutCoverCandidates`、`TestDownloadCoverArtifactsRejectsInvalidCandidateAndNormalizesFormats`、`TestDownloadCoverArtifactsRetriesTransientCandidateFailure`、`TestCoverCandidatesExhaustsHighResolutionBeforeNormalCovers`、`TestCoverCandidatesAddsDMMFallbackForStandardDVDID`、`TestDownloadExtraFanartsRejectsInvalidPreservesExistingAndContinues`、`TestValidateAndNormalizeRejectInvalidImagesWithoutOutput`、`TestNormalizeEncodesBytesMatchingDestinationExtension` |
| 海报裁剪、方向处理、字幕/无码标签 | `internal/poster` | `TestProcessCropsWideFanartAndAddsLabel`、`TestNormalizeAppliesJPEGEXIFOrientation`、`TestProcessCompositesAlphaMark` |
| 人脸优先裁剪、标签开头模式匹配与默认回退 | `internal/poster`、`cmd/javsp/main.go` | `TestPigoDetectorSelectsHighestScoreFace`、`TestProcessFallsBackWhenPigoDetectorFindsNoFace`、`TestBuildPosterOptionsUsesPigoForNativeAndLegacyEngineNames`、`TestShouldUsePosterFaceCropMatchesOnlyAtLabelStart`、`TestShouldUsePosterFaceCropIgnoresInvalidPattern`、`TestPosterSourceContainsNoSubprocessDetector` |
| 读取 JavDB 浏览器 Cookie | `internal/browsercookies` | `TestReadJavDBCookieDBFiltersAndDecryptsCookies`、`TestProviderReturnsCookiesOnce` |
| 版本文本与经共享代理访问 Go 发布源的只读更新检查 | `cmd/javsp/main.go`、`internal/updatecheck` | `TestVersionTextIncludesBuildMetadata`、`TestCheckForUpdatesPassesBuildVersion`、`TestCheckDefaultsToGoProjectReleaseFeed`、`TestCheckDetectsNewRelease`、`TestFormatResult` |

## 已注册站点爬虫

下表列出全部 20 个当前可从配置选择并注册的站点。除为补齐官网元数据而增加的 `faleno` 外，每个条目对应原项目 `javsp/web/<id>.py` 的解析工作流、Go 的 `internal/crawler/sites/<id>.go` 和具名测试。

| ID | Go 测试 |
| --- | --- |
| `airav` | `TestAirAVParse` |
| `avsox` | `TestAVSoxParse` |
| `avwiki` | `TestAVWikiParse` |
| `dl_getchu` | `TestDLGetchuParse` |
| `fanza` | `TestFanzaVideoaParse`、`TestFanzaSearchFallbackParsesAnimePage` |
| `faleno` | `TestFalenoParse`、`TestFalenoNotFound` |
| `fc2` | `TestFC2Parse`、`TestFC2HardworkingPreviewVideo` |
| `fc2fan` | `TestFC2FanLocalMirrorParse` |
| `fc2ppvdb` | `TestFC2PPVDBParse` |
| `gyutto` | `TestGyuttoParse` |
| `jav321` | `TestJav321Parse` |
| `javbus` | `TestJavBusParse` |
| `javdb` | `TestJavDBParseSearchAndDetail`、`TestJavDBRetriesWithProviderCookiesOnLoginRedirect` |
| `javlib` | `TestJavLibParseSearchResult` |
| `javmenu` | `TestJavMenuParse` |
| `mgstage` | `TestMGStageParse` |
| `njav` | `TestNJAVParse` |
| `prestige` | `TestPrestigeParse` |
| `arzon` | `TestArzonParse` |
| `arzon_iv` | `TestArzonIVParse` |

`TestRegisterAllRegistersSupportedCrawlers` 另行验证该注册表包含完整集合。

## 更新行为的刻意差异

自动更新已按用户决策**刻意移除**：Go 模块不包含下载、替换或执行新二进制的生产流程，`other.auto_update` 仅保留为兼容配置键且默认 `false`。这不是待补齐项目。

`other.check_update` 是可选的**只读**查询：仅当它为 `true` 且二进制有非 `dev` 版本号时，程序读取最新发布信息并显示结果。它不会写入、下载、安装或替换任何文件。

## 小文件行为的刻意差异

按当前项目要求，`scanner.minimum_size` 是硬门槛：严格小于该值的文件不会进入番号识别或分片合并。原版会把部分小文件重新并入同番号分片；Go 版明确不保留这个例外。默认门槛为 `1GiB`。

## 对照边界

- 本表以 `javsp/__main__.py` 的普通入口及其调用链为范围。
- `config.yml` 中列出的功能以当前 Go schema、注册表和测试为准；详见 [配置说明](CONFIGURATION.md)。
- 不把旧 README 中尚未实现的能力列为差距或承诺，避免将计划当成已实现功能。
