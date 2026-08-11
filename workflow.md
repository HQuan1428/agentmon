# WORKFLOW — Solo Dev (Superpowers)

## 0. Triết lý thiết kế

Quy trình này dành cho **một người tự làm việc một mình**: không luật hóa qua constitution, không dùng artifact đặc tả tách riêng nhiều tầng (kiểu spec/plan/tasks với nhiều gate xác minh). Đặc tả — nếu cần — chỉ tồn tại dưới dạng **một plan ngắn gọn duy nhất** (xem mục 2), vì không có nhiều người/nhiều agent cần đồng bộ qua một nguồn luật chung.

Dù gọn, quy trình vẫn giữ nguyên 2 nguyên tắc sống còn của tầng thực thi — chi phí gần như bằng 0 dù làm một mình, nên không có lý do bỏ:

- **TDD không khoan nhượng**
- **Bằng chứng thay lời tuyên bố**

Việc production-critical, cần nhiều người/nhiều agent cùng tuân thủ một bộ luật chung, hoặc quá lớn/quá mơ hồ để gói gọn trong một plan duy nhất → nằm ngoài triết lý thiết kế của quy trình này và cần một tầng đặc tả + quản trị nghiêm ngặt hơn (constitution, nhiều gate xác minh) trước khi code.

Cần công cụ riêng cho dự án (design sync, security review, đọc code base lớn...) → gắn thêm skill/plugin/MCP tương ứng, xem mục 3. Không sửa khung ở đây.

---

## 1. Định tuyến (10 giây, 3 cấp)

| Cấp | Khi nào | Việc phải làm |
|---|---|---|
| **0 — Trivial** | Không đổi hành vi: typo, docs, comment, format | Sửa thẳng, test suite hiện có phải vẫn xanh |
| **1 — Fix / thay đổi nhỏ** | Hành vi đã rõ, sửa 1 chỗ, không mơ hồ | `systematic-debugging` (nếu là bug) → regression test FAIL (RED) → fix tối thiểu (GREEN) → refactor → review |
| **2 — Feature** | Hành vi mới, chạm nhiều chỗ, hoặc còn mơ hồ | Toàn bộ mục 2 |

**Nghi ngờ giữa 2 cấp → hỏi luôn**, đừng tự đoán: "Việc này chạm module X và Y, mình đi Cấp 1 hay 2?" — đang ngồi cạnh nhau, hỏi rẻ hơn nhiều so với làm sai cấp. Chỉ khi agent chạy autonomous/background không có ai để hỏi thì mới fallback: chọn cấp cao hơn.

**Việc phình to giữa chừng** (đang Cấp 1 mà đụng module thứ 2, hoặc phát sinh câu hỏi thiết kế) → DỪNG, nâng lên Cấp 2, viết plan trước khi tiếp tục.

**Cấp 0/1 không phải là vô luật** — TDD và bằng chứng vẫn bắt buộc, cái được bỏ chỉ là giấy tờ đặc tả.

---

## 2. Quy trình Cấp 2 (Feature) — dùng Superpowers làm khung plan + thực thi

`writing-plans` của Superpowers đóng vai trò plan chính thức cho feature — đây là **nguồn lệnh duy nhất** cho toàn bộ quá trình thực thi, không cần thêm artifact đặc tả nào khác.

1. **Còn mơ hồ?** → `superpowers:brainstorming` để làm rõ ý định trước khi viết plan. Ngắn/cùng phiên thì dùng luôn; dài hoặc sẽ tiếp tục ở phiên khác → lưu ra file trong `docs/plans/` (đừng tin trí nhớ session, session có thể bị compact).
2. **Viết plan** → `superpowers:writing-plans`. Plan phải có: task 2–5 phút/task, một trách nhiệm mỗi task, file path cụ thể, bước verify, thứ tự test-first.
3. **Cần cách ly?** → `superpowers:using-git-worktrees` nếu feature đủ lớn để muốn chạy song song hoặc dễ rollback. Không bắt buộc với việc nhỏ.
4. **Thực thi** → `superpowers:subagent-driven-development` (cùng phiên) hoặc `superpowers:executing-plans` (cần checkpoint qua nhiều phiên). Mỗi task: RED → GREEN → REFACTOR → **review 2 tầng** (đúng ý định của plan trước, chất lượng code sau).
5. **Bug khó phát sinh giữa chừng** → `superpowers:systematic-debugging`. Cấm vá triệu chứng, phải tìm root cause.
6. **Phát hiện plan sai/thiếu giữa chừng** → DỪNG task, sửa plan.md trước, không "tiện tay" code khác plan.
7. **Xong** → `superpowers:requesting-code-review` (thêm 1 lớp review độc lập nếu muốn) → merge → xóa worktree (nếu có dùng).

**DoD:** mọi task trong plan có bằng chứng — test pass + review pass. "Tôi kiểm tra rồi" không phải bằng chứng.

---

## 3. Mở rộng theo dự án — skill/plugin/MCP phụ

Quy trình này chỉ quy định khung kỷ luật tối thiểu; công cụ domain-specific của từng dự án gắn thêm vào đây khi cần, **không sửa khung ở mục 1–2**:

- Cần đồng bộ thiết kế UI ↔ code → skill/MCP dự án tự định nghĩa (vd. `DesignSync`).
- Cần review bảo mật trước khi merge phần nhạy cảm → `security-review`.
- Cần hiểu code hiện có trước khi sửa → `codegraph` (nếu repo có `.codegraph/`) hoặc agent `Explore`.
- Việc lặp định kỳ (dọn log, kiểm tra CI, poll trạng thái...) → skill `loop` hoặc `schedule`.
- Convention riêng của dự án (naming, error handling, logging...) → đóng gói bằng `writing-skills`, commit vào `.claude/skills/` — để không phải nhắc lại mỗi phiên.

Nguyên tắc: thêm 1 dòng vào mục này khi thêm công cụ mới, không viết lại workflow.

---

## 4. Vibe Mode

Khai báo tường minh: *"Vibe mode: thử nghiệm X"* → tạo worktree/branch `spike/<ten>`, tắt hết gate, code tự do. Mục đích hợp lệ: thăm dò công nghệ, prototype, học API mới.

**Luật duy nhất: code spike KHÔNG BAO GIỜ merge vào main.** Học xong → vứt code, quay lại Cấp 1/2 làm lại tử tế theo đúng quy trình ở mục 1–2.

---

## 5. Anti-pattern (rút gọn cho solo)

| Anti-pattern | Fix |
|---|---|
| Tự đoán cấp thay vì hỏi khi mơ hồ | Hỏi 1 câu ngắn — rẻ hơn nhiều so với làm sai cấp |
| Coi "task xong" bằng lời tuyên bố | Bằng chứng bắt buộc: test pass + review pass |
| Vá triệu chứng bug thay vì tìm root cause | `systematic-debugging` trước khi sửa |
| Fix nhỏ phình to thành feature mà không dừng lại | DỪNG, nâng cấp, viết plan.md trước khi tiếp tục |
| Merge code từ branch spike vào main | Cấm tuyệt đối — spike chỉ để học, không để giữ |
| Plan.md cũ đã lệch code thực tế mà không cập nhật | Cập nhật plan hoặc ghi chú lệch ngay khi phát hiện |
| Bỏ regression test vì "chỉ sửa vài dòng" | TDD là hằng số ở mọi cấp, kể cả Cấp 1 |

---

## 6. Checklist khởi động nhanh

```
□ Triage 10s → chọn Cấp 0/1/2 (mơ hồ → hỏi, không tự đoán)
□ Cấp 0: sửa thẳng, test suite vẫn xanh — xong
□ Cấp 1: systematic-debugging → RED → GREEN → refactor → review — xong
□ Cấp 2:
   □ (mơ hồ?) brainstorming → neo vào file nếu dài/khác phiên
   □ writing-plans (task 2–5 phút, file path, verify, test-first)
   □ (đủ lớn?) using-git-worktrees
   □ subagent-driven-development hoặc executing-plans
   □ mỗi task: RED-GREEN-REFACTOR + review 2 tầng
   □ plan sai giữa chừng? → DỪNG → sửa plan → tiếp tục
   □ requesting-code-review trước khi merge → xóa worktree
□ Cần công cụ riêng dự án? → thêm vào mục 3, không sửa khung
```
