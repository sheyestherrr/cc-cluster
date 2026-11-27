// 表单控件的校验状态与读屏软件的对接。
// 样式由 CSS 的 :user-invalid 决定（填写并离开，或尝试提交之后才标出）；浏览器不会据此设置 ARIA 属性，这里补上：
// aria-invalid 与 :user-invalid 保持一致；所在 Field 的错误说明只在显示期间列入 aria-describedby
// （被 aria-describedby 引用的文字即使隐藏也会被读出）；离开控件后出现的错误经页面上唯一的提示区域读出。

type Control = HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;

const isControl = (el: EventTarget | null): el is Control => el instanceof Element && el.matches("input, select, textarea");

/** 控件所在 Field 的错误说明；没有时为 null。 */
const errorOf = (el: Control) => el.closest(".field-group")?.querySelector<HTMLElement>(".field-error") ?? null;

/** 读屏软件读到的文字：图标换成它的文字替代。 */
function spokenText(node: HTMLElement): string {
  const copy = node.cloneNode(true) as HTMLElement;
  copy.querySelectorAll("[role=img][aria-label]").forEach((n) => n.replaceWith(`${n.getAttribute("aria-label")}：`));
  return (copy.textContent ?? "").replace(/\s+/g, " ").trim();
}

/** 开始维护整个页面上表单控件的校验状态。只调用一次。 */
export function watchFormValidity(): void {
  const announcer = document.createElement("div");
  announcer.setAttribute("aria-live", "polite");
  announcer.className = "sr-only";
  document.body.append(announcer);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let announcedField: Control | null = null;

  // 由这里标为无效的控件；别处设置的 aria-invalid="true" 不动。
  const flagged = new WeakSet<Control>();
  // 当前的错误已经读出（或因提交失败而获得焦点、由浏览器读出）的控件。
  const announced = new WeakSet<Control>();

  const link = (el: Control, shown: boolean) => {
    const id = errorOf(el)?.id;
    if (!id) return;
    const ids = (el.getAttribute("aria-describedby") ?? "").split(/\s+/).filter((x) => x && x !== id);
    if (shown) ids.push(id);
    if (ids.length) el.setAttribute("aria-describedby", ids.join(" "));
    else el.removeAttribute("aria-describedby");
  };

  const announce = (el: Control) => {
    const error = errorOf(el);
    if (!error) return;
    const label = el.getAttribute("aria-label");
    clearTimeout(timer);
    announcer.textContent = "";
    announcedField = el;
    // 稍等片刻，让读屏软件先读完接着获得焦点的控件。
    timer = setTimeout(() => (announcer.textContent = `${label ? `${label}：` : ""}${spokenText(error)}`), 500);
  };

  const sync = (target: EventTarget | null, speak = false) => {
    if (!isControl(target)) return;
    if (target.matches(":user-invalid")) {
      if (speak && !announced.has(target)) {
        announce(target);
        announced.add(target);
      }
      target.setAttribute("aria-invalid", "true");
      link(target, true);
      flagged.add(target);
    } else if (flagged.has(target) || target.getAttribute("aria-invalid") !== "true") {
      target.setAttribute("aria-invalid", "false");
      link(target, false);
      flagged.delete(target);
      announced.delete(target);
      if (announcedField === target) {
        clearTimeout(timer);
        announcer.textContent = "";
      }
    }
  };

  // 弹窗里的控件是后来才出现的：获得焦点时补上初始的 aria-invalid="false"。
  document.addEventListener("focusin", (e) => sync(e.target));
  document.addEventListener("blur", (e) => sync(e.target, true), true);
  document.addEventListener("change", (e) => sync(e.target, true), true);
  document.addEventListener("input", (e) => sync(e.target));
  // 提交失败时浏览器对每个不合格的控件触发 invalid（不冒泡），并把焦点移到第一个：它的错误由焦点读出，这里不再重复。
  document.addEventListener(
    "invalid",
    (e) => {
      if (isControl(e.target)) announced.add(e.target);
      // :user-invalid 在事件之后才生效。
      setTimeout(() => sync(e.target));
    },
    true,
  );
}
