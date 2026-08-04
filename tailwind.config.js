/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
    "node_modules/daisyui/dist/**/*.js",
    "node_modules/react-daisyui/dist/**/*.js",
  ],
  theme: {
    extend: {
      // Mirrors src/lib/z-layers.ts — keep the two in lockstep so a class and
      // an inline style never disagree about stacking order.
      zIndex: {
        dropdown: "1000",
        popover: "1100",
        tooltip: "1200",
        toast: "1300",
      },
    },
  },
  plugins: [require("daisyui")],
  daisyui: {
    themes: require("./src/app/themes").themes,
  },
};
