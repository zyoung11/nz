# n0

n0 是个人用的虚拟局域网工具。一台服务器，多台机器，把你的设备连成一个虚拟局域网。n0 从 nz 分叉，是协议重做的大版本，只支持 Linux 和 Windows。

## 编译

前置条件：Go 版本不小于 1.26。

```bash
go build -ldflags="-s -w" -o n0 ./cmd/n0
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o n0.exe ./cmd/n0
```

## 快速上手

服务器端 `config.json`：

```json
{
  "mode": "server",
  "port": 4399,
  "subnet": "192.168.200.0/24",
  "admin": {"name": "me", "password": "strong-password"}
}
```

节点端 `config.json`：

```json
{
  "mode": "node",
  "user": "me",
  "password": "strong-password",
  "name": "laptop",
  "domain": "vps.example.com",
  "port": 4399
}
```

部署步骤：

1. 把 `n0` 和 `config.json` 放到同一目录。
2. 服务器运行 `sudo n0 install --config config.json`。
3. 你的机器运行 `sudo n0 install --config config.json`。
4. 机器首次启动时自动生成密钥，写入 `state.json`，随后自动入网。

## 配置文件

| 字段 | 模式 | 必填 | 说明 |
| --- | --- | --- | --- |
| `mode` | 全部 | 是 | `server` 或 `node` |
| `port` | 全部 | 否 | 默认 `4399` |
| `subnet` | server | 否 | 默认 `192.168.200.0/24` |
| `admin` | server | 是 | 管理员账号，含 `name` 和 `password` |
| `user` | node | 是 | 所属用户名 |
| `password` | node | 是 | 用户密码 |
| `name` | node | 是 | 机器名，同一用户内唯一 |
| `domain` | node | 是 | 服务器地址，域名或 IP |

服务器读取 `admin` 作为管理员账号。管理员的机器注册后自动批准。节点保存 `Argon2id(password, salt)` 验证值，两端都不保存密码原文。

## 命令参考

| 命令 | 作用 |
| --- | --- |
| `n0 run --config <path>` | 前台运行，日志输出到终端 |
| `n0 start --config <path>` | 后台运行，立即返回 |
| `n0 stop --config <path>` | 停止常驻进程 |
| `n0 reload --config <path>` | 校验新配置，通过后应用 |
| `n0 status --config <path>` | 显示进程与连接状态 |
| `n0 list --config <path>` | 显示机器列表 |
| `n0 users [--pending]` | 显示用户与机器全表 |
| `n0 install --config <path>` | 注册开机自启 |
| `n0 uninstall` | 移除开机自启 |
| `n0 allow <target>` | 批准申请 |
| `n0 deny <target>` | 拒绝申请 |
| `n0 block <target>` | 封禁 |
| `n0 unblock <target>` | 解除封禁 |
| `n0 trust <target>` | 加入信任，放行连管理员的机器 |
| `n0 untrust <target>` | 移出信任 |

目标语法统一：`friend` 指用户，`friend/laptop` 指机器。管理命令只对管理员开放。

## 加入流程

朋友的机器需要你批准才能入网。流程如下。

1. 朋友写节点 `config.json`，`user` 填自己的用户名，`password` 填自己的密码。
2. 朋友运行 `n0 start --config config.json`。机器生成密钥并发送申请。
3. 你运行 `n0 users --pending`，查看申请。
4. 你运行 `n0 allow friend`，批准用户。
5. 你运行 `n0 allow friend/laptop`，批准机器。服务器推送批准通知。
6. 机器收到通知，立即入网。你无需让朋友重启。

推送丢失时机器每 5 秒重发申请。朋友加第二台机器时重复第 1 至 6 步。用户已批准时只批机器。

## 连接策略

| 源机器 | 目标机器 | 默认 |
| --- | --- | --- |
| 管理员的机器 | 任何机器 | 放行，支持点对点（P2P） |
| 同一用户的机器 | 同一用户的机器 | 放行，支持 P2P |
| 普通用户的机器 | 管理员的机器 | 拒绝，`trust` 放行 |
| 用户甲的机器 | 用户乙的机器 | 拒绝 |

`trust` 放行后双向互通，支持 P2P。

单向放行的对强制中继。发起方可以呼叫对方，对方只能应答，不能反向发起。判定依据是包的性质：TCP 的 SYN 和 ICMP 的请求算发起，其余算应答。

## 安全机制

1. 每台机器独立密钥对，注册时登记公钥。机器名是标签，身份是公钥。
2. 数据端到端加密。密钥交换用椭圆曲线迪菲-赫尔曼（ECDH），每对机器一把会话密钥。
3. 路由头带机器签名，服务器只读路由头做策略，不读数据体。
4. 网际协议（IP）地址由服务器分配并绑定公钥，机器不能自选。
5. 中继包按签名身份判源，包内声明的源地址不可信。
6. 信任和封禁按公钥判定，改机器名无效。

## 服务集成

Windows 端 `n0 install` 注册 Windows 服务。服务在开机时启动，早于用户登录。失败恢复策略自动重启进程，退避上限 30 秒。

Linux 端 `n0 install` 生成 systemd unit，含 `Restart=always`。无 systemd 的机器运行 `n0 start`，配任意 init。

## 日志

默认不写日志文件。需要时给 `run` 或 `start` 加 `--log`，日志写入二进制同目录的 `n0.log`。

| 前缀 | 含义 |
| --- | --- |
| `[i]` | 状态信息 |
| `[+]` | 操作成功 |
| `[!]` | 警告 |
| `[-]` | 错误 |

## 与 nz 并行共存

n0 与 nz 可同时运行在一台机器上，互不干扰。协议版本不同，两个网络不能互通。

| 项 | nz | n0 |
| --- | --- | --- |
| UDP 端口 | `4242` | `4399` |
| 网段 | `192.168.100.0/24` | `192.168.200.0/24` |
| TUN 设备名 | `nz0` | `n0` |
| 状态文件 | `nz-state.json` | `state.json` |
| 协议版本 | `0x02` | `0x03` |

## 测试

测试用 podman 建 4 个容器。前置条件：podman、Go。

```bash
cd test
./run-all.sh
```

覆盖范围：申请批准、IP 分配、互通、方向阻断、封禁、信任、伪造防护、与 nz 共行。不覆盖 Windows 服务、wintun 驱动、真实 NAT 打洞。

## 术语表

| 术语 | 英文 | 定义 |
| --- | --- | --- |
| 网络 | network | 一台服务器和全部机器构成的整体 |
| 用户 | user | 持有密码的账号，机器的属主 |
| 机器 | machine | 一台接入网络的设备 |
| 常驻进程 | daemon | 提供转发和管理接口的后台进程 |
| 管理接口 | admin socket | 本机内部通信通道 |
| 点对点连接 | P2P | 机器之间不经过服务器的直连 |
| 中继 | relay | 流量经过服务器转发的连接方式 |
| 信任 | trust | 允许机器主动呼叫管理员的授权条目 |
| 批准 | approve | 管理员同意申请的动作 |
| 验证值 | verifier | 由密码派生的单向校验数据 |
