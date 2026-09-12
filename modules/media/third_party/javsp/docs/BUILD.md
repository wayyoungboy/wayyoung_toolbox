# JavSP-Go 构建与发布

**更新日期：2026-07-12**

本项目要求 Go 1.22。以下命令均在项目根目录执行；构建和发布过程只读取本项目中的文件。

## 开发命令

```sh
make build       # CGO_ENABLED=0，输出 bin/javsp（Windows 为 bin/javsp.exe）
make run         # 使用 ./config.yml 运行 ./cmd/javsp
make fmt         # gofmt -w 本项目内除 bin/dist 外的全部 Go 文件
make fmt-check   # 检查 gofmt；失败时打印文件路径
make vet         # go vet ./...
make test        # go test ./...
make check       # fmt-check、vet、test、主机构建
make clean       # 删除 bin/、dist/、javsp 和 javsp.exe
```

`make build` 使用 `-trimpath`，并通过链接器写入 `main.version`、`main.commit` 和 `main.buildDate`。默认值来自当前 Git 描述、短提交 ID 和 UTC 时间，也可明确覆盖：

```sh
make build VERSION=v1.2.3 COMMIT=abc1234 BUILD_DATE=2026-07-12T00:00:00Z
./bin/javsp -version
```

`VERSION` 必须是单个安全路径组件：只能包含 ASCII 字母、数字、点、下划线、加号和连字符；不能为空，不能包含 `/`、反斜杠、`..`、控制字符或其他字符。`make build` 和 `make dist` 会在创建 `bin/` 或清理、创建 `dist/` 之前调用同一个打包脚本验证入口。可单独检查版本值：

```sh
scripts/package.sh --validate-version v1.2.3-rc.1+build_7
```

## 真实网络集成夹具

`make integration-test` 调用恢复安全的 `scripts/integration-test.sh`，它不属于 `make check`。该脚本只使用项目根目录的 `config.yml`，并对每次运行通过 `-o` 覆盖测试所需设置；不会把代理地址写入默认配置。

夹具要求项目根目录同时存在 `run_test_back/ATID-687-FHD.mp4`、`run_test/` 和 `integration-output/`。这三个根目录必须是已存在的普通目录，不能是符号链接；脚本用 `pwd -P` 取得物理路径，并拒绝相同或嵌套的备份、工作、输出目录，确保备份不在任何删除、移动或归档目标内。运行前可只做无副作用的物理路径与六文件清单检查：

```sh
make integration-test INTEGRATION_ARGS=--validate-only
```

清单按文件名排序，记录每个条目的相对名、`regular-file` 类型、大小和 SHA-256；原始夹具只能有六个普通文件，不能包含目录或符号链接。脚本在每次恢复后将工作清单与初始备份清单逐项比较；失败或中断时也会恢复并校验工作目录。XML 验证必须有 `xmllint`，缺少该工具会明确失败而不会退化为标记文本检查。

脚本在任何工作副本删除或移动之前安装退出、HUP、INT、TERM 恢复处理；每个阶段开始前均从备份恢复 `run_test/`，优先使用 macOS 的 `cp -cR` 克隆复制，失败时使用 `cp -pR`。完整运行将清单写入 `integration-output/manifests/`。成功完成最后的整理阶段后，结果会保留在 `run_test/#整理完成/` 供人工检查；需要重新取得六文件原始夹具时执行 `make integration-test INTEGRATION_ARGS=--restore-only`。

先只验证恢复与哈希（不访问网络）：

```sh
make integration-test INTEGRATION_ARGS=--restore-only
```

完整测试需要在运行时提供 SOCKS5 代理；该变量没有项目内默认值：

```sh
JAVSP_TEST_PROXY=http://127.0.0.1:7897 make integration-test
```

完整运行依次构建，再对完整的六文件夹具执行 ATID-687 非破坏性抓取、Google 标题/剧情翻译、以及按默认模板移动到 `run_test/#整理完成/{actress}/[{num}] {title}` 的整理阶段。扫描器使用配置中的 `minimum_size: 1GiB` 忽略另外五个小文件，整理成功后这些文件仍以原大小和 SHA-256 留在 `run_test/`。脚本会保存 `build.log`、`smoke.log`、`google-translation.log`、`organizer.log` 和 `assertions.log` 到 `integration-output/`，并把最终视频、NFO、fanart 和 poster 留在 `run_test/#整理完成/`；测试会检查 NFO XML、fanart/poster JPEG、核心番号、整理后的视频大小及 SHA-256，以及五个被忽略文件未发生变化。开始新的完整运行前，已有日志会移至 `integration-output/archive/<UTC 时间>-<pid>/`。站点返回的日期、类型和演员仅记录为诊断，因实时站点资料会变化。

每条外部命令均受限，默认上限为 900 秒；若实时站点较慢，可仅在运行时调整：

```sh
JAVSP_TEST_PROXY=http://127.0.0.1:7897 \
  JAVSP_INTEGRATION_TIMEOUT_SECONDS=1200 \
  make integration-test
```

## 六平台发布

一次构建并打包 macOS、Linux、Windows 的 amd64 和 arm64：

```sh
make clean
make dist VERSION=v1.2.3
make checksums
scripts/verify-release.sh dist
```

`make dist` 对全部目标设置 `CGO_ENABLED=0`，使用与主机构建相同的 `-trimpath` 和版本链接参数。Unix 归档名为 `javsp-go_<version>_<goos>_<goarch>.tar.gz`，Windows 归档名为 `javsp-go_<version>_<goos>_<goarch>.zip`。

每个归档只包含一个同名顶层目录，其根目录包含 `javsp`（Windows 为 `javsp.exe`）、`config.yml`、`data/`、`image/`、`README.md`、`docs/`、`LICENSE` 和 `THIRD_PARTY_NOTICES.md`。`make checksums` 验证六个归档后生成 `dist/SHA256SUMS`，清单只覆盖这些发布归档。

下载或传输发布文件后，在 `dist/` 中验证清单。GNU/Linux 使用：

```sh
cd dist
sha256sum -c SHA256SUMS
```

macOS 使用：

```sh
cd dist
shasum -a 256 -c SHA256SUMS
```

两条命令都应报告六个归档为 `OK`；`SHA256SUMS` 不包含其他文件。

完整发布验证命令为：

```sh
make clean check
make dist
make checksums
scripts/verify-release.sh dist
test "$(find dist -maxdepth 1 -type f \( -name '*.zip' -o -name '*.tar.gz' \) | wc -l | tr -d ' ')" = 6
```

可直接检查归档文件列表：

```sh
tar -tzf dist/javsp-go_<version>_linux_amd64.tar.gz
unzip -Z1 dist/javsp-go_<version>_windows_amd64.zip
```

## 独立 CI

`.github/workflows/ci.yml` 使用 Go 1.22，并执行：

```sh
make check
```

第二个 CI 作业以六项矩阵设置 `CGO_ENABLED=0`、`GOOS` 和 `GOARCH`，再运行：

```sh
mkdir -p bin/ci
go build -trimpath -o "bin/ci/javsp-${GOOS}-${GOARCH}" ./cmd/javsp
```

CI 不访问项目外路径，也不运行真实网络集成夹具。
