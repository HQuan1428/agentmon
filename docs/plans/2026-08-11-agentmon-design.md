# agentmon — Thiết kế (Design Spec)

- **Ngày**: 2026-08-11
- **Loại việc**: Cấp 2 (Feature) theo `workflow.md`
- **Trạng thái**: đã brainstorm xong, chờ review trước khi viết plan (`superpowers:writing-plans`)

## 1. Mục tiêu

Một tool **TUI** tên `agentmon` "lắng nghe" tất cả các session Claude Code đang chạy
trên máy và hiển thị trạng thái làm việc của mỗi session bằng một **thanh trụ ngang**
lấp đầy dần theo tiến trình. Người dùng nhìn một màn hình là biết session nào đang
chạy, chạy tới đâu, session nào đã xong hoặc đang chờ mình, mà không phải mở từng
cửa sổ.

Nghe (chuông) khi có sự kiện cần chú ý: một session **hoàn thành**, hoặc một bg job
**đang chờ user quyết định**.

## 2. Nguồn dữ liệu (đã xác minh trên máy)

Tool là **read-only poller** trên `~/.claude/`, không cần IPC.

| Nguồn | Nội dung dùng |
|---|---|
| `~/.claude/sessions/<pid>.json` | Registry mọi session đang chạy: `pid`, `sessionId`, `cwd`, `kind` (`interactive`/`bg`), `status` (`busy`/`idle`), `name`, `jobId` (bg), `updatedAt`. |
| `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl` | Transcript. Dùng để (a) lấy TodoWrite todos → progress, (b) phát hiện subagent (`Task`/`Agent` tool_use chưa có `tool_result`). `encoded-cwd` = thay mọi `/` trong `cwd` bằng `-` (vd `/home/HQuan/x` → `-home-HQuan-x`). |
| `~/.claude/jobs/<jobId>/state.json` | Chỉ bg job. `state` (`running`/`blocked`/`idle`/`done`...), `detail`, `needs`, `inFlight.tasks`/`queued`, `children`. |

**Chỉ có `busy`/`idle`** trong `sessions/*.json` — không có trạng thái "chờ approval"
tường minh cho session interactive. Đây là ràng buộc chi phối mục 5C.

`~/.claude/tasks/<id>/` trên thực tế rỗng (chỉ `.lock`/`.highwatermark`) → **không dùng**.

## 3. Kiến trúc

Ba lớp tách bạch, test độc lập; ngôn ngữ **Go + Bubble Tea**.

```
┌─────────────┐  poll ~1s   ┌──────────────┐  view      ┌────────────┐
│  collector  │ ──────────▶ │    model     │ ─────────▶ │   render   │
│ (đọc FS)    │  []Session  │ (state+anim) │  tick ~100ms│ (thanh+cây)│
└─────────────┘             └──────────────┘            └────────────┘
                                   │ edge-detect
                                   ▼
                            ┌────────────┐
                            │   sound    │  ting-ting
                            └────────────┘
```

**Cấu trúc package:**
```
agentmon/
  main.go                 // wiring + cobra flags (--no-sound, --interval)
  internal/collector/     // đọc FS → []Session          (test nặng, fixtures)
  internal/model/         // Bubble Tea Model + edge-detect chuông
  internal/render/        // vẽ thanh trụ + cây subagent
  internal/sound/         // synth ting-ting
```

## 4. Mô hình dữ liệu

```go
type ProgressMode int
const ( Determinate ProgressMode = iota; Indeterminate )

type Session struct {
    ID        string        // sessionId
    Name      string        // "improve-finbert-error-handling"
    Project   string        // basename(cwd) — để gom nhóm & làm header
    Cwd       string
    Kind      string        // "interactive" | "bg"
    Status    string        // "busy" | "idle"
    JobState  string        // bg only: "running"|"blocked"|"idle"|"done"|""
    PID       int
    Mode      ProgressMode
    Done, Total int         // chỉ dùng khi Determinate
    Blocked   bool          // bg: JobState=="blocked" → cần user quyết định
    NeedsHint string        // bg: state.json.needs (mô tả ngắn cần gì)
    Children  []Session     // subagent (đệ quy 1 tầng là đủ cho hiện tại)
    UpdatedAt int64
}
```

`isDone(s)` (dùng cho progress 100% và edge-detect chuông):
- Determinate → `Total > 0 && Done >= Total`.
- Indeterminate → `Status == "idle"` (idle = coi như xong task).
- bg job → `JobState == "done"` hoặc `JobState == "idle"`; **`blocked` KHÔNG phải done**.

## 5. Quy tắc nghiệp vụ

### 5A. Xác định progress (auto-detect, trong collector)

1. Đọc `sessions/<pid>.json`. Lọc **PID chết** bằng `syscall.Kill(pid, 0)` (ESRCH → bỏ).
2. Nếu `kind == "bg"` và có `jobs/<jobId>/state.json`:
   - `JobState` = `state`; `Blocked` = `state=="blocked"`; `NeedsHint` = `needs`.
   - Progress (thứ tự ưu tiên):
     1. Nếu `detail` khớp regex `(\d+)/(\d+)\s+tasks` (vd `"pipeline 41/41 tasks done"`)
        → **Determinate** `Done/Total` từ 2 số đó.
     2. Ngược lại nếu `inFlight.tasks + inFlight.queued > 0` → **Indeterminate** (đang chạy).
     3. Ngược lại → **Indeterminate**, dựa `isDone` theo `JobState` (done/idle = đặc 100%).
3. Ngược lại (interactive) tìm transcript theo `encoded-cwd` + `sessionId`:
   - Quét **lần gọi TodoWrite cuối cùng** → `input.todos[]`.
     `Total = len`, `Done = số status=="completed"` → **Determinate**.
   - Không có TodoWrite nào → **Indeterminate**.
4. Subagent (interactive): trong transcript, mỗi `Task`/`Agent` tool_use là 1 con;
   `tool_use.id` **chưa** có `tool_result` tương ứng = **đang chạy** (Indeterminate,
   sweep); đã có result = **done**. `Name` con = `input.description`; loại =
   `input.subagent_type`.
5. Subagent (bg): từ `state.json.children` nếu có.

**Đọc transcript hiệu quả**: file có thể lớn (MB). Nhớ **offset đã quét** của từng
file giữa các lần poll và chỉ đọc phần **mới** (tail), không parse lại từ đầu mỗi giây.
Trạng thái tổng hợp (todo cuối, tập tool_use/tool_result) được giữ lũy kế trong collector.

### 5B. Ngữ nghĩa thanh loading

| Trường hợp | Thanh | Nhãn |
|---|---|---|
| Determinate, busy | đặc tới `Done/Total`, đầu sóng ▓ sau mép đặc | `6/10` |
| Determinate, đã done / idle | đặc 100%, không sóng | `done` |
| Indeterminate, busy | khối ▓▓▓ lướt qua lại (sweep) trên nền ⋮ | `sweep` |
| Indeterminate, idle | đặc 100% | `done` |
| bg, blocked | đặc theo progress hiện có, **không sóng**, dấu ⏸ | `⏸ blocked` |
| PID chết | (bị lọc, không hiển thị) | — |

### 5C. Chuông

Edge-detect trong `model`, so hai snapshot poll liên tiếp:

- **Chuông DONE**: một dòng (session **hoặc** subagent) chuyển `!isDone → isDone`.
  Phát **đúng 1 lần** tại khoảnh khắc chuyển; giữ done không phát lại.
- **Chuông APPROVAL**: **chỉ bg job**, chuyển `!blocked → blocked`. Motif khác chuông done.
- **Interactive chờ trả lời**: session interactive dừng để hỏi user → `status` thành
  `idle` → theo mô hình được coi là done → **phát chuông DONE** (không phân biệt được
  "chờ trả lời" vs "đã xong"; chấp nhận, vì hành động của user là như nhau: quay lại
  session đó). Không đầu tư IPC để tách bạch ở phiên này.

**Âm chuông** (`internal/sound`): synth PCM sine tại chỗ, **không file ngoài**.
- Dịu, nhẹ: nốt ~E5–A5 (660–880 Hz), 2 tiếng "ting-ting" ngắn ~120ms mỗi tiếng,
  biên độ nhỏ (~0.2), có **fade-out** để không gắt. Tránh tần số quá cao/chói.
- Hai motif phân biệt bằng tai: DONE (đi lên, vd E5→A5), APPROVAL (nhắc lại 2 nốt
  cùng cao, vd A5–A5) — miễn khác nhau rõ.
- Hàng đợi **non-blocking**, **gộp** nếu nhiều sự kiện trùng thời điểm (không ting chồng).
- Phát nền qua `hajimehoshi/oto` (hoặc tương đương). **Fallback**: nếu init audio thất
  bại (thường gặp trên WSL2 không có audio server) → log **một lần**, tự tắt tiếng,
  **không crash**. Cờ `--no-sound` tắt hẳn; phím `s` bật/tắt lúc chạy.

## 6. Render (ví dụ)

```
▸ da_cnm/project
   improve-finbert         ██████▓⋮⋮⋮⋮⋮⋮   6/10    busy
      ├─ ⌁ general-purpose · Implement Task 7   ▓▓▓⋮⋮⋮⋮⋮   sweep
      └─ ⌁ Review Task 6                         ████████   done
   719f75d8 (bg)           ████████████████   41/41   ⏸ blocked
                           needs: commit now? / restore AGENTS.md ...

▸ monitor-multi-agent
   monitor-multi-agent-54  ████▓⋮⋮⋮⋮⋮⋮⋮⋮   sweep   busy
```

Ký tự: `⋮` ô rỗng, `█` ô đặc, `▓` đầu sóng / khối sweep. Nhóm theo `Project`, có header
`▸`. Subagent lồng dưới cha bằng nhánh cây `├─ / └─`.

## 7. Nhịp & tương tác

- `pollTick` ~**1s** (`--interval` chỉnh được): collector quét lại → cập nhật list +
  edge-detect chuông.
- `animTick` ~**100ms** (10fps): chỉ đẩy pha đầu sóng/sweep, không đọc file.
- Phím: `q`/`Ctrl-C` thoát · `s` bật/tắt tiếng · `↑/↓` cuộn · `c` gập/mở cây subagent.

## 8. Kiểm thử (TDD — bắt buộc theo workflow.md)

- **collector** (test nặng nhất): dựng fixture thư mục `.claude` giả — `sessions/*.json`,
  transcript `.jsonl` mẫu (có/không TodoWrite; có `Task` chưa/có result),
  `jobs/<id>/state.json` (running/blocked). Assert `[]Session`: đúng Mode, Done/Total,
  Children, Blocked; PID chết bị lọc; đọc-tail theo offset cho kết quả bằng đọc-lại-toàn-bộ.
- **model**: feed 2 snapshot liên tiếp → assert edge-detect:
  `!done→done` phát đúng 1 DONE; done giữ done không phát lại; `!blocked→blocked` phát
  APPROVAL; gộp nhiều sự kiện thành hàng đợi hợp lệ.
- **render**: cho `Session` cố định → assert chuỗi thanh trụ chính xác (`██████▓⋮⋮`),
  layout cây, nhãn.
- **Thủ công (không unit-test)**: phát âm thanh thật, vòng lặp Bubble Tea, fallback audio
  trên WSL2.

Mỗi task trong plan: RED → GREEN → REFACTOR → review 2 tầng. DoD: test pass + review pass.

## 9. Ngoài phạm vi (YAGNI)

- Không IPC/socket/hook để phân biệt "chờ trả lời" vs "done" cho interactive.
- Không ghi/không sửa gì trong `~/.claude` (read-only tuyệt đối).
- Không cấu hình màu/theme, không lưu lịch sử, không export.
- Cây subagent đệ quy nhiều tầng: hiện chỉ cần 1 tầng con; sâu hơn để sau nếu cần.
