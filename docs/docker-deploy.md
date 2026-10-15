# nowen-video Docker 部署指南

本 Fork 在原版基础上增加了：**软/硬件解码与硬件加速可手动/强制选择**、**播放器画质档位与自动清晰度**；
**v0.4.0** 起内置 **ffmpeg-over-ip 客户端**，支持「nowen 容器无 GPU、转码在另一台 GPU 主机执行」的远端 GPU 方案，并修复了 v0.3.0 在 alpine 容器里挂载宿主 ffmpeg 导致的 **HTTP 500**。

> ⚠️ 关键前提：这些改造只存在于本 Fork（`github.com/Leondanxi/nowen-video`）。
> 任何包含改造代码的镜像都必须从本 Fork 构建；直接 `docker pull cropflre/nowen-video:latest` 拉到的是**上游原版**。

---

## 形态一：远端 GPU（推荐，适配你的 AIO + Windows 显卡直通）

**架构**：CT/Docker 里跑 nowen（无 GPU），内置 ffmpeg-over-ip 客户端；
直通显卡的 Windows VM 里跑 ffmpeg-over-ip 服务端；所有 ffmpeg/ffprobe 调用经内网 TCP 转发到 GPU 主机，文件经连接回传、服务端不落盘。

```
Docker（无 GPU）：nowen + ffmpeg-over-ip 客户端  ──TCP 5050──▶  Windows VM：ffmpeg-over-ip 服务端 ──▶ RTX 2070 Super (NVENC)
```

### 第 1 步：在 GPU 主机部署服务端

Windows（你的场景）：以管理员打开 PowerShell，执行仓库内脚本（或用官方 `irm https://ffmpeg-over-ip.com/install-server.ps1 | iex`）：

```powershell
.\deploy\ffmpeg-over-ip-server\install-server-windows.ps1 -AuthSecret "你的长随机密钥" -Port 5050
```

自检：`.\ffmpeg.exe -hide_banner -encoders | findstr nvenc` 应列出 `h264_nvenc/hevc_nvenc/av1_nvenc`。
完整说明（含 Linux 服务端、防火墙、验证）见 [`deploy/ffmpeg-over-ip-server/README.md`](../deploy/ffmpeg-over-ip-server/README.md)。

### 第 2 步：改两处配置，一键启动 nowen

编辑 `docker-compose.foip.yml`：

1. `FFMPEG_OVER_IP_CLIENT_ADDRESS=192.168.1.100:5050` → 改成 Windows VM 的内网 IP；
2. `FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=change-me` → 改成与服务端一致的密钥；
3. 媒体挂载那行左边 `/volume1/Media` 改成你 CT 里的媒体目录。

然后：

```bash
docker compose -f docker-compose.foip.yml up -d --build
docker compose -f docker-compose.foip.yml logs -f
```

浏览器打开 `http://<host>:8080`，默认管理员 **admin / admin123**（首次登录强制改密）。

> 等价的 `docker run`：

```bash
docker build -f Dockerfile.foip -t nowen-video:foip .
docker run -d --name nowen-video --restart unless-stopped \
  -p 8080:8080 -e TZ=Asia/Shanghai \
  -e FFMPEG_OVER_IP_CLIENT_ADDRESS=192.168.1.100:5050 \
  -e FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=你的密钥 \
  -e NOWEN_TRANSCODE_HW_DECODE_MODE=hardware \
  -e NOWEN_TRANSCODE_HW_ENCODER=nvenc \
  -v "$PWD/data:/data" -v "$PWD/cache:/cache" \
  -v /volume1/Media:/media:ro \
  nowen-video:foip
```

### 为什么 v0.3.0 会 HTTP 500、v0.4.0 如何修复

v0.3.0 的精简镜像**不含 ffmpeg**，要求把宿主 ffmpeg 挂进 alpine(musl) 容器；宿主若是常见的 glibc 版 ffmpeg，
其动态链接器在 musl 容器里无法执行 → 扫描/播放时 ffprobe/ffmpeg 启动失败 → **接口 500**。

v0.4.0 直接内置 ffmpeg-over-ip 官方**静态编译**的客户端（`ffmpeg-over-ip-client`，仅约 3MB），
并以 `ffmpeg` / `ffprobe` 两个软链形式放在 PATH 中。静态二进制不依赖 glibc/musl，在 alpine 里即可运行，
**无需再挂载任何宿主 ffmpeg，也不依赖 CT 里有没有显卡**。

---

## 形态二：本机 GPU（容器主机本地就有显卡）

适合 nowen 与 GPU 在同一台机器。用官方全功能镜像（含 ffmpeg + 驱动），NVIDIA 需装好 nvidia-container-toolkit：

```bash
docker build -f Dockerfile -t nowen-video:full .
docker run -d --name nowen-video --restart unless-stopped \
  --gpus all -p 8080:8080 \
  -e NOWEN_TRANSCODE_HW_DECODE_MODE=hardware -e NOWEN_TRANSCODE_HW_ENCODER=nvenc \
  -v "$PWD/data:/data" -v "$PWD/cache:/cache" \
  -v /volume1/Media:/media:ro \
  nowen-video:full
```

Intel QSV/VAAPI：`-e NOWEN_TRANSCODE_HW_ENCODER=qsv`（或 `vaapi`）并映射 `-v /dev/dri:/dev/dri`；
纯 CPU：`-e NOWEN_TRANSCODE_HW_DECODE_MODE=software`，去掉 GPU 参数。

---

## 形态三：自备 ffmpeg（高级，`Dockerfile.slim`）

仅当你确有自己的 ffmpeg 且**确认它是静态/兼容 musl 的二进制**时使用。⚠️ 直接挂载 glibc 动态 ffmpeg 到 alpine 正是 v0.3.0 的 500 根因，普通用户请直接用形态一。

```bash
docker compose -f docker-compose.app.yml up -d --build   # 记得把 ffmpeg/ffprobe 卷挂载改成你自己的静态二进制
```

---

## 环境变量速查

| 变量 | 默认 | 说明 |
|---|---|---|
| `NOWEN_APP_PORT` | `8080` | Web/API 监听端口 |
| `NOWEN_APP_DATA_DIR` | `/data` | 数据目录（持久化） |
| `NOWEN_DATABASE_DB_PATH` | `/data/nowen.db` | SQLite 路径 |
| `NOWEN_CACHE_CACHE_DIR` | `/cache` | 转码缓存目录（持久化） |
| `NOWEN_LOGGING_LEVEL` | `info` | 日志级别 |
| `NOWEN_TRANSCODE_HW_DECODE_MODE` | `auto` | `auto` / `software` / `hardware`（强制硬件） |
| `NOWEN_TRANSCODE_HW_ENCODER` | `auto` | `nvenc` / `qsv` / `vaapi` / `amf` |
| `NOWEN_APP_FFMPEG_PATH` | `ffmpeg` | ffmpeg 路径；形态一已默认指向 ffmpeg-over-ip 客户端 |
| `NOWEN_APP_FFPROBE_PATH` | `ffprobe` | ffprobe 路径；形态一已默认指向 ffmpeg-over-ip 客户端 |
| `FFMPEG_OVER_IP_CLIENT_ADDRESS` | （必填） | GPU 服务端地址 `<host>:5050`（形态一） |
| `FFMPEG_OVER_IP_CLIENT_AUTH_SECRET` | （必填） | 与服务端一致的 HMAC 密钥 |
| `FFMPEG_OVER_IP_CLIENT_FALLBACK_TO_LOCAL` | `false` | 服务端不可用是否回退本机 ffmpeg（容器内无 ffmpeg，保持 false） |
| `FFMPEG_OVER_IP_CLIENT_DEBUG` | `false` | 客户端打印连接/命令细节（排障用） |
| `PUID` / `PGID` | `0`/`0` | 运行属主（0 = root） |
| `TZ` | `Asia/Shanghai` | 时区 |

---

## 显卡能力边界（RTX 2070 Super，Turing / TU104）

- H.264 编码 `h264_nvenc`（浏览器通用，默认）、HEVC 编码 `hevc_nvenc`；解码走 NVDEC（`-hwaccel cuda`）。
- **不支持 AV1 硬件编/解码**：AV1 片源会自动回退为 CPU 解码 → GPU 编码成 AVC/HEVC，不会生成 `av1_nvenc`。

---

## 部署后检查清单

- [ ] `docker ps` 中 nowen 状态正常（Up），`curl http://127.0.0.1:8080/api/health` 正常。
- [ ] 浏览器 `http://<host>:8080` 能打开，用 admin/admin123 登录并改密。
- [ ] 媒体目录已挂到 `/media:ro`，Web 里能扫到文件（扫描走的是远端 ffprobe）。
- [ ] 形态一：服务端地址/密钥正确；播放需要转码的影片时，Windows VM 的 `nvidia-smi` 出现 `ffmpeg.exe`、Video Encode 有占用，服务端日志含 `-hwaccel cuda … h264_nvenc …`。
- [ ] 播放器控制栏可切换 640p/720p/1080p/2K/4K/原画，「自动」档在限速时能逐级降级、恢复后升回。
