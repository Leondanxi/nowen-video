# ffmpeg-over-ip 服务端部署（GPU 主机）

nowen 的 Docker 镜像里**只内置了 ffmpeg-over-ip 客户端**；真正的转码发生在
**另一台有 GPU 的机器**上的 ffmpeg-over-ip 服务端。你的场景是：

```
PVE 主机
├── CT-A（Docker）          跑 nowen-video（内置 ffmpeg-over-ip 客户端，无 GPU）
│        内网 TCP 5050  ───────────────────────┐
└── Windows VM（显卡直通）                       │  命令上行 / 文件双向隧道
        └── ffmpeg-over-ip 服务端  <────────────┘
             └── Jellyfin-ffmpeg（原生 h264_nvenc / hevc_nvenc / av1_nvenc）
                 └── 调用 RTX 2070 Super（NVENC / CUDA）
```

要点：

- 客户端把 ffmpeg/ffprobe 的**命令行参数**经 TCP 转发给服务端；
- 服务端用自带的 Jellyfin-ffmpeg（已编译 NVENC/NVDEC/CUDA）执行；
- 输入/输出文件通过**同一条连接**回传，**服务端不落盘**——所以媒体文件只需要
  在 nowen 容器里能看到即可，不需要在 GPU 主机上共享；
- 多客户端各自独立进程，互不影响；
- 通信使用 HMAC-SHA256 共享密钥，**两端密钥必须一致**。

---

## 一、Windows（推荐，适用于你的 2070 Super 直通 Windows VM）

前置：Windows VM 已安装最新 NVIDIA 驱动（GeForce / Studio 驱动即可，
普通 GeForce 驱动默认开放 NVENC）。

### 方式 A：一键脚本（推荐）

以**管理员**身份打开 PowerShell，进入本目录，执行：

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
.\install-server-windows.ps1 -AuthSecret "换成你自己的长随机密钥" -Port 5050
```

脚本会下载官方服务端 + Jellyfin-ffmpeg、生成 `ffmpeg-over-ip.server.jsonc`、
放行防火墙 5050 端口，并在前台启动服务。

### 方式 B：官方交互脚本

```powershell
irm https://ffmpeg-over-ip.com/install-server.ps1 | iex
```

按提示输入监听 IP（默认 `0.0.0.0`）、端口、密钥即可。

### 自检 GPU/NVENC

在服务端目录执行，应当能看到 `h264_nvenc / hevc_nvenc / av1_nvenc`：

```powershell
.\ffmpeg.exe -hide_banner -encoders | findstr nvenc
```

转码时在 VM 内打开任务管理器 → GPU → 查看 **Video Encode / Decode** 是否有占用，
或执行 `nvidia-smi`，应能看到 `ffmpeg.exe` 进程。

> 若脚本启动后又在前台退出，可用生成的 `start-server.bat` 常驻；
> 也可把它注册为「任务计划程序」开机任务或用 NSSM 注册为 Windows 服务。

---

## 二、Linux（可选，仅当服务端装在 Linux GPU 主机）

前置：宿主机装 NVIDIA 驱动 + `nvidia-container-toolkit`。

```bash
# 验证 GPU 已透传进容器
docker run --rm --gpus all nvidia/cuda:12.4.1-runtime-ubuntu22.04 nvidia-smi

# 构建并启动服务端
cd deploy/ffmpeg-over-ip-server
# 先改 docker-compose.yml 里的 FFMPEG_OVER_IP_SERVER_AUTH_SECRET
docker compose up -d --build
docker compose logs -f        # 看到 listening 即成功
```

---

## 三、nowen 客户端（Docker，CT-A）连接参数

服务端就绪后，在 **nowen 容器**侧配置（`docker-compose.foip.yml` 已带）：

```yaml
- FFMPEG_OVER_IP_CLIENT_ADDRESS=<Windows-VM 的内网IP>:5050
- FFMPEG_OVER_IP_CLIENT_AUTH_SECRET=<与服务端一致的密钥>
- NOWEN_TRANSCODE_HW_DECODE_MODE=hardware
- NOWEN_TRANSCODE_HW_ENCODER=nvenc
```

nowen 根目录一键启动：

```bash
docker compose -f docker-compose.foip.yml up -d --build
```

首次启动默认管理员 `admin / admin123`（首次登录强制改密）。

---

## 四、验证是否真的走了远端 GPU

1. 播放一个视频，并在播放器里把画质切到非「原画」档位（强制转码），
   或在「设置」里确认转码后端为 NVENC；
2. Windows VM 上 `nvidia-smi` 出现 `ffmpeg.exe`，Video Encode 占用上升；
3. 服务端开 `debug` 后，日志会打印每条命令，含 `-hwaccel cuda … h264_nvenc …`；
4. nowen 容器内**没有**本地 ffmpeg，能正常播放即证明全部走的是远端。

---

## 五、常见问题

| 现象 | 排查 |
|---|---|
| nowen 播放/扫描 500，日志报 ffmpeg 连接失败 | 客户端连不上服务端：检查地址、5050 端口、防火墙、密钥是否一致 |
| 服务端日志出现 `unknown encoder 'h264_nvenc'` | 服务端 ffmpeg 没识别到 GPU：检查 VM 显卡驱动、显卡是否真的直通、Windows 设备管理器里是否有显卡 |
| 延迟高、起播慢 | 确保客户端与服务端在同一局域网（千兆以上），勿走公网/无线 |
| 容器启动后扫描卡住 | 把 `FFMPEG_OVER_IP_CLIENT_DEBUG=true` 打开，客户端会打印连接细节 |
| 想强制不回退 CPU | 保持 `FFMPEG_OVER_IP_CLIENT_FALLBACK_TO_LOCAL=false`（容器内本就无 ffmpeg） |

> 关于 `rewrites`（命令重写）：这是我们在**无 GPU 的 CI/沙箱**里，
> 为验证「命令是否正确派发」而在**服务端**临时把 `h264_nvenc` 等价成
> `libx264` 的调试手段。**真机 GPU 部署切勿配置 rewrites**，否则会退化成软件编码。
> 本目录 `ffmpeg-over-ip.server.jsonc` 默认不含任何 rewrites，可直接用于真机。
