# ffmpeg-over-ip 部署指南（远程 GPU 转码：nowen 容器 + Windows GPU VM）

> 适用分支：`feat/hwaccel-quality`。本指南与仓库冻结规格 `FEATURE_SPEC.md`（§1、§3、§4、§9、§14）保持一致。
>
> **本环境验证状态**：Dockerfile / compose / 本文档在本仓库产出；**没有**在此环境实际构建镜像、也**没有**连接到带 RTX 2070 Super 的 Windows VM，因此「client ↔ server ↔ NVENC」的端到端连通与真实编解码**未验证**。文中所有需要你落地的地址、密钥、路径都用 `<...>` 占位。请按文末「用户自测清单」逐项验证。

---

## 1. 它解决什么问题

你的 PVE all-in-one 把 **RTX 2070 Super（Max-Q，Turing TU104）直通给了一台 Windows VM**，那台 VM 上有 GPU；而 nowen 跑在**另一台没有显卡的 Linux CT 的 Docker** 里。

ffmpeg-over-ip 让「没有 GPU 的容器」里的 ffmpeg 调用，透明地转发到「有 GPU 的 Windows VM」上的真实 ffmpeg，由 VM 调用 NVENC/NVDEC 完成编解码。容器**不需要** NVIDIA runtime、设备挂载、CUDA 库对齐，也**不需要** NFS/SMB 共享存储——文件 I/O 通过同一条 TCP 连接隧道回容器本地磁盘。

---

## 2. 架构与数据/控制流

```
┌─────────────────────────── Linux CT / Docker (no GPU) ───────────────────────────┐
│                                                                                  │
│  nowen-video (Go)                                                                 │
│    │  exec "ffmpeg" / "ffprobe"   (ffmpeg_path = /opt/ffoip/ffmpeg,              │
│    ▼                                 ffprobe_path = /opt/ffoip/ffprobe)          │
│  /opt/ffoip/ffmpeg  ─── ffmpeg-over-ip client（透明 drop-in argv 转发）           │
│    │                                                                              │
│    │  ① TCP 出站连接 + HMAC-SHA256 鉴权（每条命令都签名）                          │
│    │  ② 透传 argv；stdout/stderr/exit code 实时回传                                 │
│    │  ③ 文件 I/O（open/read/write/seek/stat/unlink/rename/mkdir）隧道回本容器磁盘   │
│    ▼                                                                              │
└────│───────────────────────────────────────────────────────────────────────────────┘
     │  单一认证 TCP :5050（无 TLS，仅限可信内网）
     ▼
┌─────────────────────────── Windows VM（直通 RTX 2070 Super）──────────────────────┐
│  ffmpeg-over-ip-server.exe  ── 监听 0.0.0.0:5050                                  │
│    │  鉴权通过后，在本机拉起打过补丁的 ffmpeg.exe（jellyfin-ffmpeg 流水线）         │
│    ▼                                                                              │
│  ffmpeg.exe / ffprobe.exe（与 server.exe 同目录）                                  │
│    │  -hwaccel cuda（NVDEC 解码，帧留显存）                                       │
│    │  -c:v h264_nvenc（默认）/ hevc_nvenc（GPU 编码）                              │
│    ▼                                                                              │
│  NVIDIA 驱动 + RTX 2070 Super (Turing)                                            │
└───────────────────────────────────────────────────────────────────────────────────┘
```

### 关键事实

| 维度 | 说明 |
|---|---|
| 连接模型 | server 端唯一监听一个端口（默认 **5050**）；client 只做出站连接。多个 nowen 会话可并发连同一 server，每会话一个独立 ffmpeg 进程。 |
| 认证 | **HMAC-SHA256** 共享密钥，每条命令都签名。**没有 TLS**——密钥与媒体内容在链路上明文，**仅限可信内网（trusted LAN）**，不要暴露到公网。 |
| 文件 I/O | server 端是打过补丁的 ffmpeg，把 `file://` 层的 open/read/write/seek/stat/unlink/rename/mkdir **隧道回 client**，由 client 在容器本地磁盘读写。因此容器内**绝对路径直接可用**，无需共享挂载。 |
| 网络输入例外 | 只有 `http/rtsp/rtmp/tcp` 等**网络输入**由 Windows VM 自己去拉（此时 VM 必须可达那个远端地址）。本地媒体文件一律走容器磁盘。 |
| argv 透明性 | client 完全透传 argv；stdout/stderr/exit code 忠实回传；支持 stdin `-i -`、stdout `-`、HLS 分段写/删、mkdir、remux `-c copy`、`-progress`。 |
| ffprobe | **同一个二进制**：client 按 `argv[0]` basename 是否含 `ffprobe` 切换模式。把同一文件复制/软链为 `ffprobe` 即可，无需本地真 ffprobe。 |
| 路径约定 | 容器内统一放在 **`/opt/ffoip/ffmpeg`** 与 **`/opt/ffoip/ffprobe`**。**务必用绝对路径**。 |

---

## 3. Turing（RTX 2070 Super）编解码能力边界

这张卡是 Turing TU104，能力是硬约束，nowen 与 server 都要据此选编码器：

| 能力 | 支持？ | nowen / ffmpeg 用法 |
|---|---|---|
| H.264 编码 | ✅ `h264_nvenc` | **默认编码器**，浏览器全兼容。LAN 高画质 `-b:v 0 -rc vbr -cq <CQ>` 或码率档。 |
| HEVC 编码 | ✅ `hevc_nvenc` | HEVC 支持 B 帧；需 `browser_hevc=true` 且客户端能播 HEVC 时才下发。 |
| H.264 解码加速 | ✅ NVDEC | `-hwaccel cuda`（推荐，帧留显存配合 nvenc），或 `-hwaccel h264_cuvid`。 |
| HEVC 解码加速 | ✅ NVDEC | `-hwaccel cuda`，或 `hevc_cuvid`。 |
| **AV1 硬编** | ❌ **不支持** | AV1 硬编是 Ada/RTX40 世代才有，Turing **没有**。 |
| **AV1 硬解** | ❌ **不支持** | Turing NVDEC 不解 AV1。 |

**如何避免无效的 AV1 硬解/硬编**：当片源 codec 为 AV1、而生效后端是 nvenc/qsv/vaapi/amf 时，nowen 命令生成层（规格 §7）会**强制目标 codec 回到 H.264（或可播时的 HEVC）**，并去掉对 AV1 无效的 `-hwaccel cuda` 预参——即「AV1 源用 CPU 解码 → GPU 编码成 AVC/HEVC」，**绝不生成 AV1-nvenc 参数**。server 端无需额外改写；如确有需要也可用 server `rewrites`，但本架构默认不依赖它。

---

## 4. Windows VM 端安装（server）

### 4.1 目录布局

把三个文件放在**同一个目录**（server 会在自身二进制所在目录查找 `ffmpeg.exe` / `ffprobe.exe`）。Releases 自带打好补丁的 ffmpeg/ffprobe（jellyfin-ffmpeg 流水线，含 NVENC/QSV/VAAPI/AMF）：

```
C:\ffmpeg-over-ip\
├─ ffmpeg-over-ip-server.exe
├─ ffmpeg.exe
├─ ffprobe.exe
└─ ffmpeg-over-ip.server.jsonc
```

> 下载：https://github.com/steelbrain/ffmpeg-over-ip/releases —— Windows server 取 `windows-amd64-ffmpeg-over-ip-server.zip`（含 server + ffmpeg + ffprobe）。或用官方一键脚本 `irm https://ffmpeg-over-ip.com/install-server.ps1 | iex`。

### 4.2 配置文件 `ffmpeg-over-ip.server.jsonc`

与 `ffmpeg-over-ip-server.exe` 同目录（server 会自动查找）。JSONC 格式（允许 `//` 注释）：

```jsonc
{
  // 监听地址：0.0.0.0 = 允许内网所有网卡接入；端口固定 5050
  "address": "0.0.0.0:5050",
  // HMAC 共享密钥：必须与 nowen 容器侧 FFMPEG_OVER_IP_CLIENT_AUTH_SECRET 完全一致。
  // 用强随机串（例如 openssl rand -hex 32 的输出）。
  "authSecret": "<REPLACE_WITH_A_LONG_RANDOM_SECRET>",
  // 日志：stdout / stderr / 文件路径。排障期建议打开。
  "log": "stdout",
  // 排障期打开：打印每条命令的原始/改写后 argv。稳定后可关。
  "debug": true
  // 本架构不需要 rewrites（Turing 有 h264_nvenc/hevc_nvenc，与 nowen 请求一致）。
  // 仅当需要改译编码器时再加，例如 ["h264_nvenc", "h264_qsv"]。
}
```

> 也可以不用配置文件，直接用环境变量：`FFMPEG_OVER_IP_SERVER_ADDRESS=0.0.0.0:5050`、`FFMPEG_OVER_IP_SERVER_AUTH_SECRET=<secret>`、`FFMPEG_OVER_IP_SERVER_LOG=stdout`。

### 4.3 启动 server

在该目录开 PowerShell：

```powershell
.\ffmpeg-over-ip-server.exe --config .\ffmpeg-over-ip.server.jsonc
```

让它常驻（可注册为任务计划 / NSSM 服务）。启动后应看到监听在 `:5050`。

### 4.4 Windows 防火墙放行 5050（仅内网）

管理员 PowerShell，新增一条**仅允许内网网段**的入站规则：

```powershell
New-NetFirewallRule -DisplayName "ffmpeg-over-ip (LAN only)" `
  -Direction Inbound -Protocol TCP -LocalPort 5050 `
  -RemoteAddress 192.168.1.0/24 `
  -Action Allow
```

> 把 `192.168.1.0/24` 改成你的实际内网段。**不要**对 `Any` 开放——无 TLS，公网暴露等于裸奔。

### 4.5 确认 GPU/编码器可用

在 Windows VM 上直接跑自带的 ffmpeg.exe 确认 NVENC/NVDEC 在位：

```powershell
.\ffmpeg.exe -hide_banner -encoders | findstr /i "nvenc"
# 期望看到: V..... h264_nvenc  / hevc_nvenc
.\ffmpeg.exe -hide_banner -hwaccels
# 期望列出 cuda（及 cuvid 相关）
.\ffmpeg.exe -hide_banner -decoders | findstr /i "hevc_cuvid h264_cuvid"
```

- `h264_nvenc` = 默认、浏览器通用；`hevc_nvenc` = 可选 HEVC。
- 解码用 `-hwaccel cuda`。
- 若 `-encoders` 里**没有** `*_nvenc`：说明 Windows 侧 NVIDIA 驱动没装好 / 直通未生效，先别往下走。

---

## 5. Docker 客户端：两种接入方式

nowen 容器内 client 的落点固定为 `/opt/ffoip/ffmpeg`、`/opt/ffoip/ffprobe`。两种二选一。

### 方式 A（推荐）：只读 bind-mount，不内置进镜像

最干净、可随时换 client 版本、不污染镜像。

1. 在宿主机下载与**容器架构**匹配的 client：
   - 仓库：https://github.com/steelbrain/ffmpeg-over-ip/releases
   - 资产命名（已确认，以 `v5.2.1` 为例）：
     - x86_64：`linux-amd64-ffmpeg-over-ip-client.zip`
     - arm64：`linux-arm64-ffmpeg-over-ip-client.zip`
   - 解压出单个静态二进制，放到宿主机例如 `/opt/ffoip/ffmpeg-over-ip-client`，`chmod +x`。
2. 在 `docker-compose.yml` / `docker-compose.deploy.yml` 的 `volumes:` 取消注释：

   ```yaml
   - /opt/ffoip/ffmpeg-over-ip-client:/opt/ffoip/ffmpeg:ro
   - /opt/ffoip/ffmpeg-over-ip-client:/opt/ffoip/ffprobe:ro
   ```

   （同一文件挂两处；client 靠 argv[0] 自动切换 ffmpeg/ffprobe。`:ro` 只读。）

### 方式 B：构建时内置进镜像（`FFOIP_VERSION`）

`Dockerfile` / `Dockerfile.full` 都内置了**默认关闭**的可选构建阶段。传一个 release tag 即自动下载、放到 `/opt/ffoip/ffmpeg` 并软链 `ffprobe`：

```bash
docker build \
  --build-arg FFOIP_VERSION=v5.2.1 \
  -t nowen-video:ffoip .
```

或在 `docker-compose.yml` 的 `build.args` 里设 `FFOIP_VERSION: "v5.2.1"`。

- `FFOIP_VERSION` 留空（默认）→ 该阶段是 no-op，镜像不含 client，与现状完全一致。
- 下载 URL 按 `linux-${TARGETARCH}-ffmpeg-over-ip-client.zip` 拼（`amd64`/`arm64` 与 release 资产名一一对应）。
- **系统 ffmpeg 始终保留**（`apk add ffmpeg`），作为本地软解/回退，不受影响。

> **选用建议**：日常用 **方式 A（bind-mount）**——升级 client 只需换宿主机上的文件、重建镜像。方式 B 适合需要「一个镜像到处跑、不想额外管宿主机文件」的场景。无论哪种，**地址/密钥都不进镜像**。

### 5.1 方式 A vs 方式 B 对照

| 维度 | A. 只读 bind-mount（推荐） | B. 构建内置 FFOIP_VERSION |
|---|---|---|
| client 来源 | 宿主机手动下载解压 | Dockerfile 构建时自动从 GitHub Releases 拉 |
| 升级 client | 换宿主机文件，重启容器 | 改 `FFOIP_VERSION` 重新 build |
| 镜像可移植性 | 依赖宿主机有该文件 | 自包含 |
| 落点 | `/opt/ffoip/ffmpeg`、`/opt/ffoip/ffprobe`（:ro） | 同左（构建期 install + 软链） |
| 系统 ffmpeg 回退 | 保留 | 保留 |
| 是否含密钥 | 否（运行期注入） | 否（运行期注入） |

---

## 6. 让 nowen 用上 client（两条等价路径）

### 6.1 路径一：nowen 热设置（推荐，保存即生效，无需重启）

管理后台 → 系统设置（`/api/admin/settings/system`），按下表填写（对应冻结规格 §3.1）：

| nowen 设置键 | 填什么 | 说明 |
|---|---|---|
| `ffoip_enabled` | `true` | 视为 ffoip client；启动子进程时自动注入连接环境 |
| `ffoip_server_address` | `192.168.1.50:5050`（=你的 VM） | 映射 `FFMPEG_OVER_IP_CLIENT_ADDRESS` |
| `ffoip_auth_secret` | `<强随机密钥>` | 映射 `FFMPEG_OVER_IP_CLIENT_AUTH_SECRET`；GET 会脱敏为 `__SET__` |
| `ffmpeg_path` | `/opt/ffoip/ffmpeg` | **绝对路径**，指向 client |
| `ffprobe_path` | `/opt/ffoip/ffprobe` | 同一 client 的 ffprobe 软链 |
| `hw_decode_mode` | `hardware` | 强制走远端 GPU（即使本地探测不到 GPU） |
| `hw_encoder` | `nvenc` | 远端 Turing 用 NVENC |
| `gpu_fallback_cpu` | `false` | 本用户默认：失败即报错，不静默回退 CPU |

### 6.2 路径二：compose 环境变量注入

在同 compose 文件旁建 `.env`（**不要提交 git**）：

```dotenv
FFOIP_SERVER_ADDRESS=192.168.1.50:5050
FFOIP_AUTH_SECRET=<REPLACE_WITH_A_LONG_RANDOM_SECRET>
```

然后取消 compose 里这几行的注释：

```yaml
environment:
  - FFMPEG_OVER_IP_CLIENT_ADDRESS=${FFOIP_SERVER_ADDRESS}
  - FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=${FFOIP_AUTH_SECRET}
  - FFMPEG_OVER_IP_CLIENT_LOG=/cache/ffmpeg-over-ip.client.log
```

> **两种路径如何叠加**：nowen 在 `ffoip_enabled=true` 时会把 address/secret 作为子进程 env **追加**到 `os.Environ()` 之后（规格 §9）。也就是说——热设置里填了就用热设置；热设置留空时，子进程继承容器里的 `FFMPEG_OVER_IP_CLIENT_*` 环境。二选一即可，都填时热设置优先。
>
> **注意媒体路径必须是容器内绝对路径**（如 `/media/电影/a.mkv`）。文件读/写由 client 在容器本地完成；只有 `http/rtsp/rtmp/tcp` 网络输入才由 Windows VM 去拉。

---

## 7. 验证

### 7.1 client `-version` 应打印 Windows server 的 ffmpeg 构建

这是最关键的一步：`-version` 经 client 转发，打印的应是 **Windows VM 上那台 ffmpeg 的 build**（含 nvenc、平台标识），而不是容器里系统 ffmpeg 的版本。

```bash
# 进入容器
docker exec -it nowen-video sh

# 注入连接环境后调用 client（也可直接用热设置后的 nowen，这里手动验证 client 本身）
export FFMPEG_OVER_IP_CLIENT_ADDRESS=192.168.1.50:5050
export FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=<secret>
export FFMPEG_OVER_IP_CLIENT_LOG=/cache/ffmpeg-over-ip.client.log

/opt/ffoip/ffmpeg -version
# 期望：第一行是 Windows/jellyfin-ffmpeg 的构建串，而不是 Alpine 的 ffmpeg 8.1.2
/opt/ffoip/ffprobe -version
# 期望：同样转发到 server，打印 server 端 ffprobe 构建
```

> 若打印的是 Alpine `ffmpeg version 8.1.2`，说明 client 没连上去、且走了本地回退（或你其实调到了系统 ffmpeg）。检查 address/secret/防火墙。

### 7.2 转码冒烟测试

用一个**容器内绝对路径**的小样本，强制走 NVENC：

```bash
docker exec -it nowen-video sh -c '
  FFMPEG_OVER_IP_CLIENT_ADDRESS=192.168.1.50:5050 \
  FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=<secret> \
  /opt/ffoip/ffmpeg -y \
    -hwaccel cuda -i /media/test/input.mp4 \
    -c:v h264_nvenc -b:v 2M \
    -c:a copy \
    /cache/smoke_test.mp4'
```

- 期望：正常退出（exit 0），`/cache/smoke_test.mp4` 生成；Windows VM 上 server 日志能看到这条会话被拉起。
- 在 nowen 里：放一部片，画质菜单手动选 **1080P/720P** 强制转码，观察能播、进度推进、无「硬解但无效」。

### 7.3 日志位置

| 侧 | 变量 / 位置 | 说明 |
|---|---|---|
| client（容器） | `FFMPEG_OVER_IP_CLIENT_LOG=/cache/ffmpeg-over-ip.client.log` | 写到持久化卷，`docker exec -it nowen-video tail -f /cache/ffmpeg-over-ip.client.log`。**注意**：client 是透明代理，诊断日志进 log sink，不进 ffmpeg 的 stdout/stderr。 |
| server（Windows） | 配置 `"log": "stdout"` 或 `FFMPEG_OVER_IP_SERVER_LOG` | 在 server 控制台 / NSSM 日志里看会话与 argv。 |
| 排障 | server `"debug": true` / client `FFMPEG_OVER_IP_CLIENT_DEBUG=true` | 打印原始/改写后 argv。 |

---

## 8. 排障表

| 现象 | 可能原因 | 处理 |
|---|---|---|
| client `-version` 打印的是容器本地 ffmpeg（Alpine 8.1.2），而非 Windows 构建 | ① TCP 拨号失败 + 开了 `FALLBACK_TO_LOCAL`；② 没设 address/secret；③ 实际调的是系统 ffmpeg 而非 `/opt/ffoip/ffmpeg` | 确认 `ffmpeg_path=/opt/ffoip/ffmpeg`；确认 env 注入；临时 `FFMPEG_OVER_IP_CLIENT_FALLBACK_TO_LOCAL=false` 让拨号失败直接报错而非静默回退；查 client 日志。 |
| 拨号失败 / 连接被拒（dial timeout / refused） | Windows VM:5050 不可达；防火墙未放行；server 没起；address 写错 | 在容器内 `nc -zv 192.168.1.50 5050`（或 wget/telnet）测连通；确认 server 在跑；确认 Windows 入站规则放行该内网段；确认容器出站无网络策略拦截。 |
| 鉴权失败 / 立刻断开 | client 与 server 的 `authSecret` **不一致**；含多余空格/换行 | 两端密钥逐字符核对（注意 `.env` 不要有引号/尾空格）；HMAC 不匹配会在握手阶段被拒。 |
| 段写 / HLS 写失败、`No such file or directory`、mkdir 报错 | 用了相对路径，或容器内该目录不存在/不可写 | **一律用容器内绝对路径**；确认 `/cache`、`/media` 已挂载且可写（容器以 root 跑通常 OK）。文件写由 client 在容器本地完成。 |
| 转码报错 `Unknown encoder 'h264_nvenc'` 或 NVIDIA 初始化失败 | Windows 侧驱动 / 直通未生效；server 自带 ffmpeg 无 nvenc | 回 §4.5 在 Windows 上直接跑 `ffmpeg -encoders \| findstr nvenc`；修好驱动再连。 |
| AV1 片源选了硬解却无效/报错 | Turing 无 AV1 硬编硬解 | 预期行为：nowen 应自动退回 CPU 解码 + 编码成 AVC/HEVC，**不要**手动要求 AV1-nvenc。确认后端版本已实现 AV1 边界（规格 §7）。 |
| server 离线后转码全挂 | 未开本地回退（本用户默认 `gpu_fallback_cpu=false`，强制硬件） | 这是**有意行为**（排障时便于暴露问题）。若希望 server 掉线时仍能出片，再评估 `FFMPEG_OVER_IP_CLIENT_FALLBACK_TO_LOCAL=true`（仅拨号失败时回退，mid-session 仍致命）。 |
| 高分辨率转码很慢 / 读盘延迟大 | over-IP 文件 I/O 往返开销 | server 端可调 `FFOIP_READAHEAD_BYTES`、`FFOIP_RANGE_CACHE_BYTES`（v5.2.1 默认已做预取/range cache，一般无需动）。 |

---

## 9. 用户自测清单（部署后逐项打勾）

- [ ] Windows VM：`ffmpeg-over-ip-server.exe`、`ffmpeg.exe`、`ffprobe.exe` 同目录，`ffmpeg-over-ip.server.jsonc` 已填 `0.0.0.0:5050` 与强密钥。
- [ ] Windows 防火墙：5050 仅放行内网段；`ffmpeg -encoders` 能看到 `h264_nvenc`/`hevc_nvenc`。
- [ ] server 已启动并常驻，控制台显示监听 `:5050`。
- [ ] 容器内能 `nc -zv <VM_IP> 5050` 通（出站可达）。
- [ ] 已按方式 A bind-mount 或方式 B `--build-arg FFOIP_VERSION=v5.2.1` 提供 `/opt/ffoip/ffmpeg`、`/opt/ffoip/ffprobe`。
- [ ] `.env` 里 `FFOIP_SERVER_ADDRESS` / `FFOIP_AUTH_SECRET` 已填真值，且**未提交 git**。
- [ ] nowen 热设置：`ffoip_enabled=true`、`ffmpeg_path=/opt/ffoip/ffmpeg`、`ffprobe_path=/opt/ffoip/ffprobe`、`hw_decode_mode=hardware`、`hw_encoder=nvenc`。
- [ ] `/opt/ffoip/ffmpeg -version` 打印的是 **Windows server** 的 ffmpeg 构建（不是 Alpine 8.1.2）。
- [ ] 冒烟命令（§7.2）exit 0，`/cache/smoke_test.mp4` 生成。
- [ ] 浏览器里放一部片，手动选 720P/1080P 强制转码，能流畅播、进度推进。
- [ ] （可选）把片源换成 AV1，确认没有生成 AV1-nvenc 报错、而是退回 AVC/HEVC。

---

## 10. 安全与边界提示

- **无 TLS**：HMAC 只做命令鉴权，媒体数据与密钥均明文过网。**仅可信内网**使用，不要把 5050 暴露公网。
- **密钥管理**：地址/密钥只走 `.env` 或 nowen 热设置（GET 脱敏为 `__SET__`），**绝不写进 Dockerfile / compose 明文 / 镜像层**。
- **回退语义**：本用户默认强制硬件（`gpu_fallback_cpu=false`）。开 `FALLBACK_TO_LOCAL` 前想清楚——它会在 server 不可达时静默用容器 CPU 转码，掩盖故障。
- **路径**：容器内媒体/缓存一律绝对路径；网络输入才由 VM 拉取。

---

## 11. 验证状态声明

| 项目 | 状态 |
|---|---|
| Dockerfile / Dockerfile.full 的可选 client 构建阶段 | 已写入（默认关闭，`FFOIP_VERSION` 触发） |
| docker-compose.yml / .deploy.yml 的注释化挂载与 env 示例 | 已写入 |
| Release 资产命名核对（`linux-<arch>-ffmpeg-over-ip-client.zip`，tag `v5.2.1`） | 已核对 GitHub Releases 页 |
| **实际 `docker build` 出镜像** | **未执行**（本环境要求只产出文件，不构建） |
| **client ↔ Windows GPU VM 连通、NVENC 真实编解码、HLS 分段写** | **未验证**（本环境无 Windows VM / RTX 2070 Super） |

请以第 9 节自测清单为准完成联调。
