// The coach observes real scene actions. It never performs a business action.
export function createTour({
  workspace,
  panel,
  snapshot,
  factory,
  onUpdate,
  onNext,
}) {
  let active = false,
    plan,
    index = 0,
    phase = "action",
    note = "",
    target;
  let records = [],
    expanded = [],
    frame,
    branchMode = false;
  const pendingEvents = new WeakMap();
  const coach = document.createElement("section");
  coach.className = "tour-coach";
  coach.hidden = true;
  coach.setAttribute("aria-label", "跟我操作");
  document.body.append(coach);
  const escape = (v) =>
    String(v ?? "").replace(
      /[&<>"']/g,
      (c) =>
        ({
          "&": "&amp;",
          "<": "&lt;",
          ">": "&gt;",
          '"': "&quot;",
          "'": "&#39;",
        })[c],
    );
  const current = () => plan?.steps[index];
  function clearMarks() {
    document
      .querySelectorAll(".tour-target,.tour-away")
      .forEach((e) => e.classList.remove("tour-target", "tour-away"));
    document.querySelectorAll("[data-tour-inert]").forEach((e) => {
      e.inert = false;
      delete e.dataset.tourInert;
    });
  }
  function locate(scroll = false) {
    if (!active || !plan) return;
    clearMarks();
    const step = current();
    const root = panel();
    if (!root) return;
    root.querySelectorAll("details").forEach((d) => {
      d.open = false;
    });
    if (step) {
      const actionTarget = root.querySelector(step.target);
      const resultTarget = root.querySelector(step.result || step.target);
      root
        .querySelectorAll("button,input,select,textarea")
        .forEach((control) => {
          if (
            (phase === "result"
              ? control !== resultTarget
              : control !== actionTarget) &&
            !step.visible?.some((s) => control.matches(s))
          ) {
            (control.closest("label") || control).classList.add("tour-away");
          }
          if (phase === "result" && !control.classList.contains("tour-away")) {
            control.inert = true;
            control.dataset.tourInert = "true";
          }
        });
      target = root.querySelector(
        phase === "result" ? step.result || step.target : step.target,
      );
      for (
        let d = target?.parentElement;
        d && d !== root;
        d = d.parentElement
      ) {
        if (d.tagName === "DETAILS") d.open = true;
      }
      target?.classList.add("tour-target");
    } else target = root.querySelector(".scene-heading");
    if (scroll && target) {
      const header = document.querySelector(".site-header")?.offsetHeight || 82;
      window.scrollTo({
        top: scrollY + target.getBoundingClientRect().top - header - 20,
        behavior: "instant",
      });
    }
    position();
  }
  function position() {
    if (!active || coach.hidden) return;
    const r = target?.getBoundingClientRect();
    const width = Math.min(360, innerWidth - 28);
    coach.style.width = `${width}px`;
    const h = coach.offsetHeight;
    // Narrow layouts reserve space below content. Keep the actual target above
    // the coach; desktop bubbles sit beside it whenever there is enough room.
    let left = 14,
      top = innerHeight - h - 14;
    if (r && innerWidth > 900 && r.right + width + 38 < innerWidth) {
      left = r.right + 20;
      top = Math.max(85, Math.min(r.top, innerHeight - h - 14));
    } else if (r && innerWidth > 900 && r.left > width + 35) {
      left = r.left - width - 20;
      top = Math.max(85, Math.min(r.top, innerHeight - h - 14));
    } else {
      left = Math.max(14, Math.min(r?.left || 14, innerWidth - width - 14));
      if (r && r.top < top + h && r.bottom > top) {
        if (r.top - h - 16 > 75) top = r.top - h - 16;
        else if (r.bottom + h + 16 < innerHeight) top = r.bottom + 16;
      }
    }
    coach.style.left = `${left}px`;
    coach.style.top = `${Math.max(8, top)}px`;
    document.body.style.setProperty("--tour-space", `${h + 140}px`);
    document.body.style.setProperty("--tour-coach-height", `${h}px`);
    document.body.style.setProperty(
      "--tour-header-height",
      `${document.querySelector(".site-header")?.offsetHeight || 82}px`,
    );
  }
  function render(scroll = true) {
    if (!active) return;
    const step = current();
    const missing = step && !panel()?.querySelector(step.target);
    const disabled =
      step &&
      phase === "action" &&
      panel()?.querySelector(step.target)?.disabled;
    const blocked =
      plan.blocked ||
      (missing
        ? { message: "当前目标暂不可用。可退出自由体验，或重置本场景再开始。" }
        : null);
    const text =
      blocked?.message ||
      note ||
      (step
        ? phase === "result"
          ? step.feedback
          : step.purpose
        : plan.lesson);
    coach.dataset.phase = blocked ? "blocked" : step ? phase : "complete";
    coach.dataset.step = step?.id || "complete";
    const actionControls = `<button type="button" data-tour="locate" class="action-secondary">定位当前操作</button>${step?.event === "change" ? '<button type="button" data-tour="confirm-input" class="button button-primary">填好了</button>' : ""}${step?.example ? '<button type="button" data-tour="example" class="action-secondary">填入示例</button>' : ""}`;
    const endControls = `${plan.branch ? `<button type="button" data-tour="branch" class="button button-primary">${escape(plan.branch.label)}</button>` : ""}<button type="button" data-tour="next" class="action-secondary">体验下一项</button><a href="../details.html#${escape(workspace())}">查看设计</a><a href="../performance.html#screenshots">查看实测</a>`;
    const controls = blocked
      ? '<button type="button" data-tour="reset" class="button button-primary">重置本场景并开始</button>'
      : step
        ? phase === "result"
          ? '<button type="button" data-tour="continue" class="button button-primary">看懂了，继续</button>'
          : actionControls
        : endControls;
    coach.innerHTML = `<div class="tour-heading"><strong>${step ? `第 ${index + 1} / ${plan.steps.length} 步` : "这条体验已走完"}</strong><button type="button" data-tour="close" aria-label="关闭引导，进入自由体验">×</button></div><p class="tour-title">${escape(plan.title)}</p><p class="tour-message" role="status">${escape(text)}</p>${disabled && !blocked ? '<p class="tour-hint">这个操作暂时受限。可退出检查当前状态，或重置后重走。</p>' : ""}<div class="tour-buttons">${controls}<button type="button" data-tour="close" class="tour-skip">${step ? "跳过引导，自由体验" : "回到自由体验"}</button></div>`;
    locate(scroll);
    if (phase === "result")
      coach
        .querySelector('[data-tour="continue"]')
        ?.focus({ preventScroll: true });
    onUpdate?.(view());
  }
  function view() {
    return active ? { plan, index, phase, step: current(), note } : null;
  }
  function start(branch) {
    // Explicitly preserve the visitor's scene data and observed actions.
    if (!active)
      expanded = [...(panel()?.querySelectorAll("details") || [])].map(
        (d) => d.open,
      );
    if (branch !== undefined) branchMode = branch;
    const fresh = factory(snapshot(), branchMode, records);
    // Recovery predicates capture the cancelled purchase's owner. Keep that
    // context while re-evaluating availability from the actual current state.
    if (plan?.id === fresh.id && branchMode) plan.blocked = fresh.blocked;
    else plan = fresh;
    index = plan.steps.findIndex((s) => !s.done?.(snapshot(), records));
    if (index < 0) index = plan.steps.length;
    phase = "action";
    note =
      index > 0 && index < plan.steps.length
        ? "已保留当前操作进度。" + plan.steps[index].purpose
        : "";
    active = true;
    coach.hidden = false;
    document.body.classList.add("tour-running");
    render();
  }
  function stop() {
    active = false;
    coach.hidden = true;
    clearMarks();
    document.body.classList.remove("tour-running");
    panel()
      ?.querySelectorAll("details")
      .forEach((d, i) => {
        d.open = expanded[i] || false;
      });
    onUpdate?.(null);
  }
  function reset() {
    records = [];
    plan = null;
    branchMode = false;
    index = 0;
    phase = "action";
    note = "";
  }
  function advance() {
    index++;
    while (current()?.done?.(snapshot(), records)) index++;
    phase = "action";
    note = "";
    render();
  }
  function observe(event) {
    const node = event.target.closest("button,input,select,textarea");
    if (!node || !panel()?.contains(node) || node.disabled) return;
    if (event.type === "click" && !node.matches("button")) return;
    const before = snapshot();
    const action =
      node.dataset.action ||
      node.dataset.eventAction ||
      node.dataset.likeAction ||
      node.id;
    const record = {
      action,
      target: `#${node.id}`,
      value: node.value,
      event: event.type,
      before,
    };
    const observedPlan = plan,
      observedIndex = index,
      observedScene = workspace();
    pendingEvents.set(event, () => {
      if (workspace() !== observedScene) return;
      record.after = snapshot();
      if (/^(reset|RESET)$/.test(action)) {
        reset();
        if (active) start();
        return;
      }
      records.push(record);
      if (!active || plan !== observedPlan || index !== observedIndex) return;
      const step = current();
      if (!step || phase === "result") return;
      if ((step.event || "click") === event.type && node.matches(step.target)) {
        if (step.matches(action, before, record.after, record)) {
          phase = "result";
          note = "";
        } else
          note =
            step.invalid ||
            "这一步尚未完成，请检查输入和页面中的状态提示，再试一次；也可以重置本场景。";
      }
      render(event.type !== "input");
    });
  }
  // Capture before the scene handler replaces its DOM nodes.
  for (const type of ["click", "input", "change"]) {
    document.addEventListener(type, observe, true);
    // A native event may run a microtask checkpoint between listeners. Observe
    // the result in bubbling order, after the scene has applied its action.
    document.addEventListener(type, (event) => pendingEvents.get(event)?.());
  }
  coach.addEventListener("click", (event) => {
    const action = event.target.closest("[data-tour]")?.dataset.tour;
    if (action === "close") stop();
    if (action === "continue" && phase === "result") advance();
    if (action === "locate") locate(true);
    if (action === "reset") {
      onNext("reset");
    }
    if (action === "branch") start(true);
    if (action === "next") {
      stop();
      onNext("next");
    }
    if (action === "example" && current()?.example) {
      const input = panel()?.querySelector(current().target);
      if (input) {
        input.value = current().example;
        input.dispatchEvent(new Event("input", { bubbles: true }));
        if (current()?.event === "change")
          input.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
    if (action === "confirm-input" && phase === "action") {
      panel()
        ?.querySelector(current()?.target)
        ?.dispatchEvent(new Event("change", { bubbles: true }));
    }
  });
  function schedulePosition() {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(position);
  }
  addEventListener("resize", schedulePosition);
  addEventListener("scroll", schedulePosition, true);
  addEventListener("keydown", (e) => {
    if (e.key === "Escape" && active) stop();
  });
  new MutationObserver(() => {
    if (active) {
      locate(false);
    }
  }).observe(document.querySelector("#scene-workspace"), {
    childList: true,
    subtree: true,
  });
  return {
    start,
    stop,
    reset,
    view,
    continue() {
      if (phase === "result" && active) advance();
      else locate(true);
    },
  };
}
