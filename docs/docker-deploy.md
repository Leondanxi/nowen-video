# nowen-video Docker 部署指南

本 Fork 在原版基础上增加了：**软/硬件解码与硬件加速可手动/强制选择**、**播放器画质档位与自动清晰度**。

> ⚠️ 关键前提：这些改造只存在于本 Fork 的功能分支 `feat/hwaccel-quality`（PR #1）。
> **任何包含改造代码的镜像都必须从本 Fork 构建**；直接 `docker pull cropflre/nowen-video:latest` 拉到的是**上游原版、不含改造代码**。

本指南提供两种形态：

- **形态 A（本指南首选，适配你的 AIO）**：纯“本体”镜像，**不含 ffmpeg**，运行时挂载你自己已装好的 ffmpeg / ffprobe（或远程转码 client），强制 NVENC。
- **形态 B（可选）**：内置 ffmpeg 的全功能镜像（同样从本 Fork 构建才含改造代码），适合容器主机本地就有 GPU 的场景。

---

## 形态 A：纯本体镜像 + 自备 ffmpeg（推荐）

适用：容器所在 CT/主机没有本地显卡，ffmpeg 由你准备（例如通过内网调用 Windows VM 上 RTX 2070 Super NVENC 的 client）。

### A.1 一键（compose，推荐）

仓库已提供 `docker-compose.app.yml`。在仓库根目录（`feat/hwaccel-quality` 分支）执行：

```bash
docker compose -f docker-compose.app.yml up -d --build
docker compose -f docker-compose.app.yml logs -f
```

启动前**只需改一处**：把 `docker-compose.app.yml` 里 ffmpeg / ffprobe 卷挂载**冒号左边**改成你宿主机上真实的二进制路径（默认占位是 `/usr/bin/ffmpeg`、`/usr/bin/ffprobe`），媒体目录那行左边同理。

浏览器打开 `http://<host>:8080`，**第一个注册的账号自动成为管理员**。

### A.2 免 clone，直接从 GitHub 一键构建本体镜像

```bash
# 一条命令直接从本 Fork 的功能分支构建（无需先 git clone）
docker build -f Dockerfile.slim -t nowen-video:app \
  https://github.com/Leondanxi/nowen-video.git#feat/hwaccel-quality
```

### A.3 等价 docker build + docker run

```bash
# 1) 在仓库根目录构建本体镜像
docker build -f Dockerfile.slim -t nowen-video:app .

# 2) 运行（注意把 ffmpeg/ffprobe/媒体 的宿主机路径改成你的实际路径）
docker run -d \
  --name nowen-video \
  --restart unless-stopped \
  -p 8080:8080 \
  -e PUID=0 -e PGID=0 -e UMASK=000 \
  -e TZ=Asia/Shanghai \
  -e NOWEN_APP_PORT=8080 \
  -e NOWEN_APP_DATA_DIR=/data \
  -e NOWEN_APP_WEB_DIR=/app/web/dist \
  -e NOWEN_DATABASE_DB_PATH=/data/nowen.db \
  -e NOWEN_CACHE_CACHE_DIR=/cache \
  -e NOWEN_LOGGING_LEVEL=info \
  -e NOWEN_TRANSCODE_HW_DECODE_MODE=hardware \
  -e NOWEN_TRANSCODE_HW_ENCODER=nvenc \
  -e NOWEN_APP_FFMPEG_PATH=/ffmpeg/ffmpeg \
  -e NOWEN_APP_FFPROBE_PATH=/ffmpeg/ffprobe \
  -v ./data:/data \
  -v ./cache:/cache \
  -v /volume1/Media:/media:ro \
  -v /usr/bin/ffmpeg:/ffmpeg/ffmpeg:ro \
  -v /usr/bin/ffprobe:/ffmpeg/ffprobe:ro \
  nowen-video:app
```

> 本机无显卡、走远端 GPU：**不要**加 `--device /dev/dri`（容器内没有该节点），但**保留**
> `NOWEN_TRANSCODE_HW_DECODE_MODE=hardware` 与 `NOWEN_TRANSCODE_HW_ENCODER=nvenc`。
> 本体将按硬解参数生成、绕过 CPU 回退，实际编解码在远端 GPU 完成。

### A.4 自备 ffmpeg 挂载说明

- 挂载点与环境变量要**成对**出现：
  - 卷：`-v <宿主机ffmpeg>:/ffmpeg/ffmpeg:ro`
  - 环境变量：`NOWEN_APP_FFMPEG_PATH=/ffmpeg/ffmpeg`（ffprobe 同理）。
- 路径生效优先级：后台「设置 → 转码」里的热设置 `ffmpeg_path/ffprobe_path` ＞ 环境变量 ＞ 默认。
- 若挂载的是**动态链接**的标准 ffmpeg，启动时报缺共享库，请改用**静态编译版本 / 单文件 client**，或额外挂载其依赖库目录（可用 `ldd $(which ffmpeg)` 查看依赖）。
- 若你的 ffmpeg client 自身需要 server 地址、端口、密钥等，直接在 `docker run` / compose 的 `environment` 里追加该 client 自己的变量即可——nowen 启动的子进程会继承容器全部环境变量，本体不对任何远程方案做专门集成（也不内置任何远程转码组件）。

---

## 形态 B：内置 ffmpeg 的全功能镜像（可选）

适合容器主机本地就有 GPU、希望镜像自带 ffmpeg 的情况。注意仍需**从本 Fork 构建**才含改造代码：

```bash
# 从本 Fork 功能分支用官方 Dockerfile（内置 ffmpeg + 硬解驱动）构建
docker build -f Dockerfile -t nowen-video:full \
  https://github.com/Leondanxi/nowen-video.git#feat/hwaccel-quality

# NVIDIA 主机需先装好 nvidia-container-toolkit，并使用 --gpus all 运行：
docker run -d --name nowen-video --restart unless-stopped \
  --gpus all \
  -p 8080:8080 \
  -e NOWEN_TRANSCODE_HW_DECODE_MODE=hardware \
  -e NOWEN_TRANSCODE_HW_ENCODER=nvenc \
  -v ./data:/data -v ./cache:/cache \
  -v /volume1/Media:/media:ro \
  nowen-video:full
```

> Intel QSV / VAAPI 主机：用 `-e NOWEN_TRANSCODE_HW_ENCODER=qsv`（或 `vaapi`）并映射 `-v /dev/dri:/dev/dri`（实际设备名以宿主机为准）。
> 纯 CPU、无 GPU：`-e NOWEN_TRANSCODE_HW_DECODE_MODE=software`，去掉 GPU 相关参数。

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
| `NOWEN_APP_FFMPEG_PATH` | `ffmpeg`（PATH 内） | 自定义 ffmpeg 路径（形态 A 已默认 `/ffmpeg/ffmpeg`） |
| `NOWEN_APP_FFPROBE_PATH` | `ffprobe`（PATH 内） | 自定义 ffprobe 路径（形态 A 已默认 `/ffmpeg/ffprobe`） |
| `PUID` / `PGID` / `UMASK` | `0`/`0`/`000` | 运行属主/权限（0 = root） |
| `TZ` | `Asia/Shanghai` | 时区 |

---

## 显卡能力边界（RTX 2070 Super，Turing / TU104）

- H.264 编码 `h264_nvenc`（浏览器通用，默认）、HEVC 编码 `hevc_nvenc`（支持 B 帧）；解码走 NVDEC（`-hwaccel cuda`）。
- **不支持 AV1 硬件编/解码**：AV1 片源会自动回退为 CPU 解码 → GPU 编码成 AVC/HEVC，不会生成 `av1_nvenc`。

---

## 部署后检查清单

- [ ] `docker ps` 中 nowen-video 状态正常（Up）。
- [ ] `curl http://127.0.0.1:8080/api/health` 返回正常。
- [ ] 浏览器 `http://<host>:8080` 能打开，注册第一个账号（自动管理员）。
- [ ] 媒体目录已挂到 `/media:ro`，Web 里能扫到文件。
- [ ] 形态 A：ffmpeg/ffprobe 已挂载且 `NOWEN_APP_FFMPEG_PATH/PROBE_PATH` 指向正确；进容器 `ls -l /ffmpeg/ffmpeg` 可见、可执行。
- [ ] 播放一部需要转码的影片，确认走的是远端 NVENC（看 Windows VM 侧 GPU 占用 / client 日志），而非 CT 的 CPU。
- [ ] 播放器控制栏可切换 640p/720p/1080p/2K/4K/原画，「自动」档在限速时能逐级降级、恢复后升回。
