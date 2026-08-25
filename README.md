# Vast Cluster Bench — CPA plugin

Live benchmark dashboard cho cluster GPU Vast.ai, chạy ngay trong **CLIProxyAPI (CPA) Management UI**.

Không cần cài agent trên máy Vast — plugin SSH thẳng vào từng node (thuần Go `x/crypto/ssh`, không cần binary ssh trong container), đo:

- **Engine health** + model đang serve (auto-detect port 18000/30000/8000, auto-detect API key từ cmdline của vLLM/SGLang)
- **TTFT** và **decode tok/s** bằng một streaming request thật (`include_usage`)
- **KV cache** (dung lượng + usage), **số request đang chạy + queue** (từ `/metrics`)
- **Telemetry từng GPU** qua `nvidia-smi`: nhiệt độ, công suất, VRAM, util%
- **Giá $/h + status instance** từ Vast API
- **History 7 ngày** (SQLite-free, ring buffer trong state dir) + sparkline 24h trong dashboard

Dashboard mở ở **CPA Manager Plus → Vast Cluster Bench** (hoặc `/v0/resource/plugins/vast-cluster-bench/status`), auto-refresh 15s, nút ⚡ Probe now.

## Cài đặt (Portainer / compose)

### 1. Build

Requires Go 1.26 + CGO (Linux amd64, glibc >= 2.36):

```sh
make build VERSION=0.1.0
```

Sản phẩm: `dist/vast-cluster-bench-v0.1.0.so` — copy vào `plugins/linux/amd64/` của CPA
(mount tại `/CLIProxyAPI/plugins`).

### 2. Cấu hình CPA (`config.yaml`)

```yaml
plugins:
  enabled: true
  configs:
    vast-cluster-bench:
      enabled: true
      probe-interval: 5m        # mặc định 5 phút, có thể đổi 1m/10m...
      vast-api-key: ""          # bỏ trống nếu dùng env VAST_API_KEY
      ssh-key-path: /vast-ssh/id_ed25519   # hoặc bỏ trống + dán key trong UI
      ssh-user: root
      nodes:                    # tùy chọn: explicit list, nếu bỏ trống thì auto-discover
        - name: G
          id: "48423380"
          ssh-host: 65.95.12.163
          ssh-port: 31027
```

> **SSH path đồng bộ với vast-tunnel**: mount đúng thư mục key của tunnel
> (`/home/Docker/vast-tunnel/ssh`) vào `/vast-ssh` trong container CPA, plugin
> dùng chính key đó (`/vast-ssh/id_ed25519`) — một key cho cả tunnel lẫn plugin.

### 3. Docker mount + env (bắt buộc cho SSH key & Vast API)

Cli-proxy-api container cần:

```yaml
environment:
  - VAST_API_KEY=${VAST_API_KEY}              # dùng chung biến với vast-gateway
  - SSH_KEY_PATH=/vast-ssh/id_ed25519         # đường dẫn key trong container
volumes:
  - /home/Docker/vast-tunnel/ssh:/vast-ssh:ro   # key SSH Vast (id_ed25519)
```

> Không bắt buộc: nếu không muốn mount key, mở dashboard → ⚙ Cấu hình → dán SSH
> private key và Vast API key vào ô tương ứng (lưu vào state dir của plugin, 0600).
> **Để trống + bấm "Bỏ keys (dùng env/path)"** → plugin quay về dùng
> `VAST_API_KEY` env và `ssh-key-path`.

### 4. Restart CPA

```sh
docker restart cli-proxy-api
```

Sau restart: **CPA Manager Plus → Plugin Store → Refresh** → plugin `vast-cluster-bench`
xuất hiện, hoặc mở thẳng `/v0/resource/plugins/vast-cluster-bench/status` (nhập management key để xem data).

## Management API

| Method | Path | Mô tả |
|---|---|---|
| GET | `/v0/management/plugins/vast-cluster-bench/status` | Trạng thái mới nhất mọi node |
| GET | `/v0/management/plugins/vast-cluster-bench/history?node=G` | Lịch sử probe 1 node |
| POST | `/v0/management/plugins/vast-cluster-bench/probe` | Probe ngay lập tức |
| GET/POST | `/v0/management/plugins/vast-cluster-bench/settings` | Xem/lưu keys (UI) |

## Dev

```sh
make test        # go vet + go test
make build VERSION=0.1.0
make package VERSION=0.1.0   # zip plugin-store + checksum
```

## Phát hành qua Plugin Store (tùy chọn)

Thêm registry này vào CPA:

```yaml
plugins:
  store-sources:
    - https://raw.githubusercontent.com/ngojclee/cpa-plugin-vast-cluster-bench/main/registry.json
```

Rồi CPA Manager Plus → Plugin Store → Refresh → Install.

## License

MIT
