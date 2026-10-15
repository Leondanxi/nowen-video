# 内置的 ffmpeg-over-ip 客户端（离线构建用）

本目录随源码包内置了 ffmpeg-over-ip 官方的**静态编译客户端**，让
`Dockerfile.foip` 在构建时无需访问 GitHub 下载（适配网络受限环境）。

- 文件：`ffmpeg-over-ip-client-linux-amd64`（linux/amd64，约 3MB，静态链接，可在 alpine 运行）
- 来源：https://github.com/steelbrain/ffmpeg-over-ip/releases/latest （`linux-amd64-ffmpeg-over-ip-client.zip`）
- 用法：`Dockerfile.foip` 将其安装为 `/usr/local/bin/ffmpeg-over-ip-client`，
  并软链为 `/usr/local/bin/ffmpeg` 与 `/usr/local/bin/ffprobe`；客户端按调用名
  自动区分 ffmpeg / ffprobe 模式。
- 许可：见 `LICENSE.ffmpeg-over-ip`（ffmpeg-over-ip）与 `LICENSE.ffmpeg`（其捆绑的 FFmpeg 相关许可）。

> 架构：当前内置的是 **linux-amd64**（绝大多数 x86 服务器/CT）。如需 arm64，
> 请从上面的 release 页面下载 `linux-arm64-ffmpeg-over-ip-client.zip` 替换，
> 或在能联网的机器上直接使用早期版本那种「构建时下载」的 Dockerfile 写法。

升级方式：下载对应平台的 client zip，用里面的 `ffmpeg-over-ip-client` 替换同名文件。
