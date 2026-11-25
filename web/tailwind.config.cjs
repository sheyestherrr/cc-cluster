/** @type {import('tailwindcss').Config} */
// 颜色都指向 styles/app.css 里的变量（RGB 三元组，Tailwind 才能加透明度），取值对齐 Resin 的配色。
const token = (name) => `rgb(var(--${name}) / <alpha-value>)`;

module.exports = {
  content: ["./src/index.html", "./src/**/*.{ts,tsx}"],
  darkMode: ["selector", ".dark"],
  theme: {
    extend: {
      colors: {
        bg: token("c-bg"),
        card: { DEFAULT: token("card"), 2: token("card-2") },
        pill: { DEFAULT: token("pill"), hover: token("pill-hover") },
        line: { DEFAULT: token("line"), 2: token("line-2") },
        fg: { DEFAULT: token("fg"), 2: token("fg-2") },
        muted: token("muted"),
        faint: token("faint"),
        brand: { DEFAULT: token("brand"), soft: token("brand-soft"), fg: token("brand-fg") },
        ok: { DEFAULT: token("ok"), soft: token("ok-soft") },
        danger: { DEFAULT: token("danger"), soft: token("danger-soft") },
        warn: { DEFAULT: token("warn"), soft: token("warn-soft") },
      },
      boxShadow: {
        edge: "var(--edge)",
        card: "var(--card-edge)",
        pop: "var(--pop-shadow)",
      },
      borderRadius: { sm: "6px", md: "8px", lg: "10px", xl: "12px", "2xl": "18px" },
      fontFamily: {
        sans: ['"Manrope"', '"PingFang SC"', '"Microsoft YaHei"', "sans-serif"],
        display: ['"Sora"', '"PingFang SC"', '"Microsoft YaHei"', "sans-serif"],
        mono: ["ui-monospace", '"SF Mono"', "Menlo", "Consolas", "monospace"],
      },
      transitionTimingFunction: { soft: "cubic-bezier(0.2, 0.8, 0.2, 1)" },
      keyframes: {
        fadeIn: { "0%": { opacity: "0" }, "100%": { opacity: "1" } },
        popIn: { "0%": { opacity: "0", transform: "translateY(4px) scale(0.985)" }, "100%": { opacity: "1", transform: "none" } },
        slideIn: { "0%": { opacity: "0", transform: "translateX(16px)" }, "100%": { opacity: "1", transform: "none" } },
      },
      animation: {
        "fade-in": "fadeIn 0.16s ease-out",
        "pop-in": "popIn 0.18s cubic-bezier(0.2, 0.8, 0.2, 1)",
        "slide-in": "slideIn 0.22s cubic-bezier(0.2, 0.8, 0.2, 1)",
      },
    },
  },
  plugins: [],
};
