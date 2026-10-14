/**
 * Scrolls `list` so that `el` is visible. Unlike scrollIntoView it scrolls
 * only the list, never its scrollable ancestors, so a popup inside a dialog
 * does not shift the dialog. `list` must be the offsetParent of `el`
 * (positioned).
 */
export function reveal(list: HTMLElement | undefined | null, el: HTMLElement | null | undefined): void {
  if (!list || !el) return;
  const top = el.offsetTop;
  const bottom = top + el.offsetHeight;
  if (top < list.scrollTop) list.scrollTop = top;
  else if (bottom > list.scrollTop + list.clientHeight) list.scrollTop = bottom - list.clientHeight;
}
