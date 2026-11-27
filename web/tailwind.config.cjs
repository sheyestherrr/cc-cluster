/** @type {import('tailwindcss').Config} */
// 颜色都指向 styles/app.css 里的变量（RGB 三元组，Tailwind 才能加透明度），取值对齐 Resin 的配色。
const token = (name) => `rgb(var(--${name}) / <alpha-value>)`;

module.exports = {
  content: ["./src/index.html", "./src/**/*.{ts,tsx}"],
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
        danger: { DEFAULT: token("c-danger"), soft: token("danger-soft") },
        warn: { DEFAULT: token("warn"), soft: token("warn-soft") },
      },
      boxShadow: {
        edge: "var(--edge)",
        card: "var(--card-edge)",
        pop: "var(--pop-shadow)",
      },
      borderRadius: { sm: "6px", md: "8px", lg: "10px", xl: "12px", "2xl": "18px" },
      fontFamily: {
        sans: ['"Manrope"', '"PingFang SC"', '"Microsoft YaHei"', '"Noto Sans CJK SC"', '"Source Han Sans SC"', "sans-serif"],
        display: ['"Sora"', '"PingFang SC"', '"Microsoft YaHei"', '"Noto Sans CJK SC"', '"Source Han Sans SC"', "sans-serif"],
        mono: ["ui-monospace", '"SF Mono"', "Menlo", "Consolas", "monospace"],
      },
    },
  },
  plugins: [],
};
