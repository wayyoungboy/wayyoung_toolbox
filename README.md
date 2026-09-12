# wayyoung 的飞牛工具箱

一个面向 fnOS 的单进程工具箱。应用使用左侧标签页组织功能，每个模块有独立开关和配置，关闭的模块不会启动对应后台任务。

目前包含五个模块：

- 飞牛备份中心：创建、执行和恢复本地备份任务。
- HA500 硬件工具：查看并控制 HA500 相关硬件功能。
- 影片采集任务中心：管理 RSS、下载入库、影片整理、番剧刮削和媒体维护。
- 音乐转换中心：批量转换 NCM 等音乐文件。
- 文件完整性检查：以只读方式检查指定目录中的 PDF、图片、音视频、压缩包和常见文档是否可正常解析。

所有模块直接编译进同一个 Go 可执行文件，不会启动模块子进程或额外 Unix Socket。配置按模块隔离，日志由工具箱统一管理。首次安装时所有模块均为关闭状态。

## 安装

从 [Releases](https://github.com/wayyoungboy/wayyoung_toolbox/releases) 下载 `wayyoung_toolbox_*_x86_64.fpk`，在 fnOS 应用中心选择手动安装。

应用需要以 root 身份运行，因为 HA500 模块必须访问硬件接口。启用 HA500 模块时还需要输入 `HA500` 确认设备型号。其他模块仍执行各自的路径保护和管理员请求校验。

## 从源码构建

构建环境需要：

- Go 1.25.13
- Node.js 24 和 npm
- fnOS SDK 中的 `fnpack`（仅生成最终 FPK 时需要）

仓库包含所有自研模块和经过修改的第三方源码，不依赖本机其他项目目录：

```bash
npm test
npm run build
```

产物为 `dist/wayyoung_toolbox_0.5.1_x86_64.fpk`。没有 `fnpack` 时可以先生成完整的打包目录：

```bash
SKIP_FNPACK=1 npm run build
```

生成结果位于 `dist/stage/wayyoung_toolbox`，可复制到装有 fnOS SDK 的环境后运行 `fnpack build --directory`。

## 仓库结构

```text
cmd/                         工具箱主程序
internal/                    模块调度、统一入口和文件完整性检查
modules/backup/              飞牛备份中心
modules/ha500/               HA500 硬件工具
modules/media/               影片采集任务中心及内嵌 JAVSP
modules/music/               音乐转换中心及内嵌 ncmdump-go
packaging/wayyoung_toolbox/  fnOS 打包配置和生命周期脚本
web/                         工具箱外壳与文件完整性检查界面
```

## 安全与隐私

项目不包含示例账号、密码、个人目录或预设媒体路径。需要访问的目录、下载服务账号和翻译服务密钥均由用户在自己的 fnOS 中配置，并写入应用私有数据目录。

文件完整性检查只读取文件内容，不修改被检查的文件。发现安全问题请参阅 [SECURITY.md](SECURITY.md)。

## 参与开发

提交改动前请运行 `npm test` 和 `SKIP_FNPACK=1 npm run build`。更具体的流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

本项目整体以 [GNU General Public License v3.0](LICENSE) 发布。各内嵌模块和依赖仍保留其原有许可证，详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 以及对应源码目录中的许可证文件。
