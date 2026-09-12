# 第三方引用与许可证

本项目不是飞牛或 115 官方产品。通过飞牛现有远程挂载读写文件，不包含 rclone 可执行文件，不申请或保存 115 密码、Cookie 或访问令牌。

## 随包组件

| 组件 | 版本 | 来源 | 许可证／状态 |
| --- | --- | --- | --- |
| React、React DOM | 19.2.8 | [React](https://github.com/facebook/react) | MIT，原文随包 |
| scheduler | 0.27.0 | [React](https://github.com/facebook/react) | MIT，原文随包 |
| golang.org/x/sys | 0.35.0 | [Go sys](https://go.googlesource.com/sys/) | BSD-3-Clause，原文随包 |
| Go 标准库与运行时 | 构建所用工具链 | [Go](https://go.dev/) | BSD-style，原文随包 |
| @trimjs/web-app | 0.4.2 | [官方 SDK npm 包](https://www.npmjs.com/package/@trimjs/web-app) | 当前 npm 包未提供 LICENSE 或 package.json license 声明；不能将其声称为 MIT。公开再分发前需向权利方确认许可及其内嵌依赖条件 |

上述 SDK 是官方文档推荐的原生接口接入方式，本项目仅调用目录授权和宿主初始化能力；并非把官方 SDK 当作本项目原创代码。SDK 可能内嵌通信辅助库，其授权范围亦需一并确认。当前仅制作本地测试包，未发布公开下载。

FPK 的 `licenses/` 目录携带能取得的第三方许可证原文和本说明。工具链版本、传递依赖与完整性校验分别记录在 `go.mod`、`go.sum`、`frontend/package-lock.json`。构建工具包括 Vite、Vitest、Playwright、Testing Library；它们只用于构建／测试，不作为独立运行服务打包。

## 技术参考

- [飞牛应用开发文档](https://developer.fnnas.com/)：Manifest、生命周期、应用用户、资源声明与统一网关。
- [官方 API 调用方式](https://developer.fnnas.com/api/calling) 与 [共享目录授权](https://developer.fnnas.com/api/authorization/shared-access)：SDK 初始化、最小 Scope 和系统注入令牌。
- [rclone mount 文档](https://rclone.org/commands/rclone_mount/)：FUSE 与 VFS 写回缓存的语义。挂载写入成功不意味着最终云端持久化。

独立 Go 备份引擎、提交格式、调度与界面为本项目实现；未复制其他备份项目源代码。仅学习／测试用途的说明不替代第三方授权，也不能自动规避法律或数据风险。
