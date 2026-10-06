# nowen-video Docker 部署指南（一键起步）

> 本镜像内置了完整的转码栈，开箱即用。你只需要：把媒体目录挂进来、放行 8080、启动。
> 镜像默认从 Docker Hub 拉取：`cropflre/nowen-video:latest`（自动匹配 amd64 / arm64）。
> 也可以自行从源码构建（见文末）。

---

## 0. 前置说明

- **镜像内置 ffmpeg / ffprobe**：基础镜像经 `apk` 安装，位于 `/usr/bin/ffmpeg`、`/usr/bin/ffprobe`，已在 `PATH` 内，容器内直接写 `ffmpeg` / `ffprobe` 即可调用。**绝大多数用户无需自己准备 ffmpeg。**
- **首次启动 = 自动建管理员**：启动后访问 `http://<host>:8080`，**第一个注册的账号自动成为 admin（超级管理员）**。之后再到管理后台开放/关闭注册、邀请码。
- **硬件加速默认 NVENC**：下面示例默认 `NOWEN_TRANSCODE_HW_DECODE_MODE=hardware` + `NOWEN_TRANSCODE_HW_ENCODER=nvenc`。无 NVIDIA GPU / 纯 CPU 时，把模式改成 `software`；编码 API 可按显卡改 `auto / qsv / vaapi / amf`。
- **数据/缓存必须可写，媒体建议只读**：`/data`（数据库、配置）和 `/cache`（HLS 分片）要可写；`/media` 媒体挂成 `:ro` 更安全。

---

## 1. 方式一：一条 docker run 命令（可直接复制运行）

> 下面把宿主机 `./data`、`./cache`、`./media` 映射进容器。请按你的实际路径改 `-v` 左边（宿主机）的部分；右边（容器内）保持不变。

```bash
docker run -d \
  --name nowen-video \
  --restart unless-stopped \
  -p 8080:8080 \
  -e PUID=0 \
  -e PGID=0 \
  -e UMASK=000 \
  -e TZ=Asia/Shanghai \
  -e NOWEN_APP_PORT=8080 \
  -e NOWEN_APP_DATA_DIR=/data \
  -e NOWEN_APP_WEB_DIR=/app/web/dist \
  -e NOWEN_DATABASE_DB_PATH=/data/nowen.db \
  -e NOWEN_CACHE_CACHE_DIR=/cache \
  -e NOWEN_LOGGING_LEVEL=info \
  -e NOWEN_TRANSCODE_HW_DECODE_MODE=hardware \
  -e NOWEN_TRANSCODE_HW_ENCODER=nvenc \
  -v ./data:/data \
  -v ./cache:/cache \
  -v /volume1/Media:/media:ro \
  -v /volume1/Media/电影:/media/电影:ro \
  -v /volume1/Media/电视剧:/media/电视剧:ro \
  --device /dev/dri:/dev/dri \
  cropflre/nowen-video:latest
```

启动后：

```bash
# 看日志
docker logs -f nowen-video
# 验证健康
curl http://127.0.0.1:8080/api/health
```

浏览器打开 `http://<host>:8080` 注册第一个账号即管理员。

> **无 GPU / 纯 CPU 转码**：去掉 `--device` 那行，并把
> `-e NOWEN_TRANSCODE_HW_DECODE_MODE=hardware` 改成 `-e NOWEN_TRANSCODE_HW_DECODE_MODE=software`。
> **Intel QSV / VAAPI**：保留 `--device /dev/dri`，把编码器改成 `-e NOWEN_TRANSCODE_HW_ENCODER=qsv`（或 `vaapi`）。
>
> **本机无显卡、但通过远程 GPU 转码（例如把 ffmpeg 路径指向某个远程转码客户端）**：
> 去掉 `--device /dev/dri` 这一行（容器内没有该节点），但**保留**
> `NOWEN_TRANSCODE_HW_DECODE_MODE=hardware` 与 `NOWEN_TRANSCODE_HW_ENCODER=nvenc`（或对应 API），
> 并按第 3 节挂载你自己的 ffmpeg/ffprobe 兼容客户端、配好它自己的连接环境变量。
> 本体将按硬解参数生成、绕过 CPU 回退，实际编解码在远端 GPU 完成。

---

## 2. 方式二：docker compose（推荐，可版本化管理）

把下面内容保存为 `docker-compose.yml`，然后 `docker compose up -d`。

```yaml
# docker-compose.yml —— nowen-video 开箱即用
version: '3.8'

services:
  nowen-video:
    image: cropflre/nowen-video:latest   # 也可改成自己构建的镜像 tag
    container_name: nowen-video
    restart: unless-stopped

    ports:
      - "8080:8080"                     # Web / API

    # 容器内以 root 运行，规避 NAS 挂载目录 UID/GID 不一致
    user: "0:0"

    environment:
      - PUID=0
      - PGID=0
      - UMASK=000
      - TZ=Asia/Shanghai

      - NOWEN_APP_PORT=8080
      - NOWEN_APP_DATA_DIR=/data
      - NOWEN_APP_WEB_DIR=/app/web/dist
      - NOWEN_DATABASE_DB_PATH=/data/nowen.db
      - NOWEN_CACHE_CACHE_DIR=/cache
      - NOWEN_LOGGING_LEVEL=info

      # ---- 转码硬件加速（默认 NVENC）----
      # 解码/加速模式：auto / software / hardware
      - NOWEN_TRANSCODE_HW_DECODE_MODE=hardware
      # 硬件编码 API：nvenc(NVIDIA,默认) / auto / qsv(Intel) / vaapi(通用/AMD) / amf(AMD)
      - NOWEN_TRANSCODE_HW_ENCODER=nvenc

      # ---- 自定义 ffmpeg/ffprobe 路径（一般保持注释，用镜像内置的即可）----
      # - NOWEN_APP_FFMPEG_PATH=/usr/local/bin/ffmpeg
      # - NOWEN_APP_FFPROBE_PATH=/usr/local/bin/ffprobe

    volumes:
      # 持久化（可写）
      - ./data:/data
      - ./cache:/cache
      # 媒体目录（只读 :ro；按实际路径改左边，可挂多个）
      - /volume1/Media:/media:ro
      - /volume1/Media/电影:/media/电影:ro
      - /volume1/Media/电视剧:/media/电视剧:ro

      # ---- 自定义 ffmpeg/ffprobe（可选；取消注释才生效）----
      # - ./ffmpeg:/usr/local/bin/ffmpeg:ro
      # - ./ffprobe:/usr/local/bin/ffprobe:ro

    # 硬件加速 DRM 节点（无 GPU 可整段注释掉）
    devices:
      - /dev/dri:/dev/dri
      # FUSE（WebDAV / Alist 远程挂载才需要，宿主机先 modprobe fuse）
      - /dev/fuse:/dev/fuse

    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:8080/api/health || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 5
      start_period: 120s

    deploy:
      resources:
        limits:
          memory: 2G
        reservations:
          memory: 512M
```

常用命令：

```bash
docker compose up -d      # 启动
docker compose logs -f    # 看日志
docker compose down       # 停止
docker compose pull && docker compose up -d   # 升级到最新镜像
```

---

## 3. 自定义 ffmpeg / ffprobe（可选）

默认无需任何操作——镜像已内置 `/usr/bin/ffmpeg`、`/usr/bin/ffprobe`。

如果你想用**自己准备的** ffmpeg / ffprobe（例如自己编译的版本、含额外编解码器的版本），用**只读卷**把宿主机二进制挂进容器，再用环境变量指过去：

```yaml
    volumes:
      - ./ffmpeg:/usr/local/bin/ffmpeg:ro    # 宿主机 ffmpeg 二进制，chmod +x
      - ./ffprobe:/usr/local/bin/ffprobe:ro  # 宿主机 ffprobe 二进制
    environment:
      - NOWEN_APP_FFMPEG_PATH=/usr/local/bin/ffmpeg
      - NOWEN_APP_FFPROBE_PATH=/usr/local/bin/ffprobe
```

> 这里挂载的也可以是任意 **ffmpeg 兼容的可执行文件**（例如某种远程转码客户端），由你自己挂载并配好对应 env 即可；本镜像不对其做专门集成。

---

## 4. 环境变量速查

| 变量 | 默认 | 说明 |
|---|---|---|
| `NOWEN_APP_PORT` | `8080` | Web/API 监听端口 |
| `NOWEN_APP_DATA_DIR` | `/data` | 数据目录（持久化） |
| `NOWEN_DATABASE_DB_PATH` | `/data/nowen.db` | SQLite 路径 |
| `NOWEN_CACHE_CACHE_DIR` | `/cache` | 转码缓存目录（持久化） |
| `NOWEN_LOGGING_LEVEL` | `info` | 日志级别 |
| `NOWEN_TRANSCODE_HW_DECODE_MODE` | `auto` | `auto` / `software` / `hardware` |
| `NOWEN_TRANSCODE_HW_ENCODER` | `auto` | `nvenc` / `qsv` / `vaapi` / `amf` |
| `NOWEN_APP_FFMPEG_PATH` | `ffmpeg`（PATH 内） | 自定义 ffmpeg 可执行路径 |
| `NOWEN_APP_FFPROBE_PATH` | `ffprobe`（PATH 内） | 自定义 ffprobe 可执行路径 |
| `PUID` / `PGID` / `UMASK` | `0`/`0`/`000` | 入口脚本用的属主/权限 |
| `TZ` | `Asia/Shanghai` | 时区 |

---

## 5. 自行从源码构建（可选）

```bash
git clone <repo-url> && cd nowen-video
docker build -t nowen-video:local .          # 或 docker compose build
# 然后把上面 compose 里的 image 改成 nowen-video:local
```

---

## 6. 部署后检查清单

- [ ] `docker ps` 里 nowen-video 状态 `healthy`。
- [ ] `curl http://127.0.0.1:8080/api/health` 返回正常。
- [ ] 浏览器 `http://<host>:8080` 能打开，注册第一个账号（自动 admin）。
- [ ] 媒体目录已挂到 `/media:ro`，在 Web 里能扫到文件。
- [ ] 需要硬解时：`/dev/dri` 已映射，且 `NOWEN_TRANSCODE_HW_ENCODER` 与你的显卡匹配。
