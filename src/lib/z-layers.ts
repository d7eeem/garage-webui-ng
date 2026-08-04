/**
 * Central stacking order for overlay surfaces. Two rules:
 *
 * 1. Every floating surface takes its z-index from here — never an ad-hoc
 *    `z-10` at a call site. That is what produced the scattered patches this
 *    file replaces.
 * 2. Native <dialog> (daisyUI Modal) is NOT in this scale. Dialogs render in
 *    the browser's top layer, which paints above every z-index here no matter
 *    how large. A menu that must appear inside a dialog has to be portalled
 *    into that dialog — see `portalRoot` in components/ui/menu.tsx.
 */
export const Z_LAYERS = {
  dropdown: 1000,
  popover: 1100,
  tooltip: 1200,
  toast: 1300,
} as const;

export type ZLayer = keyof typeof Z_LAYERS;
