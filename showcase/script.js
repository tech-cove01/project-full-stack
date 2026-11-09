// Small, progressively enhanced interactions; the content remains readable without JS.
const menuButton = document.querySelector(".menu-toggle");
const menu = document.querySelector("#primary-nav");
if (menuButton && menu) {
  document.documentElement.classList.add("menu-ready");
  const closeMenu = () => {
    menuButton.setAttribute("aria-expanded", "false");
    menu.classList.remove("is-open");
  };
  menuButton.addEventListener("click", () => {
    const open = menuButton.getAttribute("aria-expanded") !== "true";
    menuButton.setAttribute("aria-expanded", String(open));
    menu.classList.toggle("is-open", open);
  });
  menu.addEventListener("click", (event) => {
    if (event.target.closest("a")) closeMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (
      event.key === "Escape" &&
      menuButton.getAttribute("aria-expanded") === "true"
    ) {
      closeMenu();
      menuButton.focus();
    }
  });
  matchMedia("(min-width: 801px)").addEventListener("change", closeMenu);
}

function enhanceSwitch(attribute, panelAttribute) {
  const buttons = [...document.querySelectorAll(`[${attribute}]`)];
  const panels = [...document.querySelectorAll(`[${panelAttribute}]`)];
  if (!buttons.length || !panels.length) return;
  const select = (value) => {
    buttons.forEach((button) =>
      button.setAttribute(
        "aria-pressed",
        String(button.getAttribute(attribute) === value),
      ),
    );
    panels.forEach((panel) => {
      panel.hidden = panel.getAttribute(panelAttribute) !== value;
    });
  };
  buttons.forEach((button) =>
    button.addEventListener("click", () =>
      select(button.getAttribute(attribute)),
    ),
  );
  select(
    buttons
      .find((button) => button.getAttribute("aria-pressed") === "true")
      ?.getAttribute(attribute) || buttons[0].getAttribute(attribute),
  );
}
enhanceSwitch("data-round", "data-round-panel");
enhanceSwitch("data-flow", "data-flow-panel");
enhanceSwitch("data-report", "data-report-panel");

const imageDialog = document.querySelector(".image-dialog");
if (imageDialog && typeof imageDialog.showModal === "function") {
  const dialogImage = imageDialog.querySelector("img");
  const caption = imageDialog.querySelector("#image-caption");
  const zoomButton = imageDialog.querySelector("[data-image-zoom]");
  const imageViewport = imageDialog.querySelector(".dialog-image-wrap");
  const setZoom = (zoomed) => {
    imageViewport.classList.toggle("is-zoomed", zoomed);
    zoomButton.setAttribute("aria-pressed", String(zoomed));
    zoomButton.textContent = zoomed ? "适应窗口" : "原尺寸查看";
    imageViewport.scrollTo(0, 0);
  };
  const toggleZoom = () =>
    setZoom(zoomButton.getAttribute("aria-pressed") !== "true");
  zoomButton.addEventListener("click", toggleZoom);
  dialogImage.addEventListener("click", toggleZoom);
  let trigger;
  document.querySelectorAll("[data-lightbox]").forEach((link) => {
    link.addEventListener("click", (event) => {
      if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
        return;
      event.preventDefault();
      trigger = link;
      dialogImage.src = link.href;
      dialogImage.alt = link.querySelector("img").alt;
      caption.textContent = link.dataset.caption;
      setZoom(false);
      imageDialog.showModal();
      document.body.classList.add("dialog-open");
    });
  });
  imageDialog
    .querySelector("[data-close-dialog]")
    .addEventListener("click", () => imageDialog.close());
  imageDialog.addEventListener("click", (event) => {
    if (event.target === imageDialog) imageDialog.close();
  });
  imageDialog.addEventListener("close", () => {
    document.body.classList.remove("dialog-open");
    trigger?.focus();
  });
}
