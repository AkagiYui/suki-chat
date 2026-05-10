import stylistic from "@stylistic/eslint-plugin"
import simpleImportSort from "eslint-plugin-simple-import-sort"
import globals from "globals"
import tseslint from "typescript-eslint"

export default [
  {
    ignores: [
      "dist/**/*",
      "node_modules/**/*",
      "public/**/*",
    ],
  },
  {
    files: ["**/*.{js,jsx,ts,tsx}"],
    plugins: {
      "@stylistic": stylistic,
      "simple-import-sort": simpleImportSort,
      "@typescript-eslint": tseslint.plugin,
    },
    rules: {
      semi: ["warn", "never"], // 禁止分号
      camelcase: ["warn", { allow: ["dead_code", "keep_classnames", "keep_fnames", "drop_console", "drop_debugger"] }], // 强制驼峰命名
      eqeqeq: ["warn", "smart"], // 智能禁止使用 == 和 !=，要求使用 === 和 !==
      "no-multi-str": ["warn"], // 禁止使用多行字符串
      "prefer-template": ["warn"], // 优先使用模板字符串
      "no-var": ["warn"], // 禁止使用 var
      "no-unused-vars": [
        "off",
        {
          vars: "all", // 变量
          args: "none", // 参数
          ignoreRestSiblings: false, // 忽略剩余的解构
          varsIgnorePattern: "required", // 忽略 required(vee-validate)
        },
      ], // 未使用的变量
      "prefer-const": ["warn", {
        "destructuring": "any",
        "ignoreReadBeforeAssign": false,
      }], // 优先使用 const

      "@stylistic/max-len": ["warn", { code: 8000 }], // 单行最大长度
      "@stylistic/no-trailing-spaces": ["warn"], // 禁止行尾空格
      "@stylistic/linebreak-style": ["warn", "unix"], // 换行符风格
      "@stylistic/no-multiple-empty-lines": ["warn", { max: 2, maxEOF: 1, maxBOF: 0 }], // 空行数量
      "@stylistic/quotes": ["warn", "double", { avoidEscape: true }], // 引号
      "@stylistic/brace-style": ["warn", "1tbs", { allowSingleLine: true }], // 大括号风格
      "@stylistic/comma-dangle": ["warn", "always-multiline"], // 逗号后面必须有空格
      "@stylistic/eol-last": ["warn", "always"], // 文件末尾必须有换行符
      "@stylistic/template-curly-spacing": ["warn", "never"], // 模板字符串中花括号内的空格
      "@stylistic/object-curly-spacing": ["warn", "always"], // 对象字面量中花括号内的空格
      "@stylistic/space-infix-ops": ["warn", { int32Hint: false }], // 运算符周围的空格
      "@stylistic/keyword-spacing": ["warn", { before: true, after: true }], // 关键字周围的空格
      "@stylistic/arrow-spacing": ["warn"], // 箭头函数的箭头前后的空格
      "@stylistic/space-before-blocks": ["warn", { functions: "always", keywords: "always", classes: "always" }], // 块语句大括号前的空格
      "@stylistic/no-multi-spaces": ["warn"], // 禁止使用多个空格
      "@stylistic/comma-spacing": ["warn", { before: false, after: true }], // 逗号周围的空格
      "@stylistic/semi-spacing": ["warn", { before: false, after: true }], // 分号周围的空格
      "@stylistic/indent": [
        "warn",
        2, // 默认缩进2个空格
        {
          SwitchCase: 1, //  switch语句缩进1个单位
          VariableDeclarator: 1, // 变量声明符缩进1个单位
          offsetTernaryExpressions: true, //三元表达式缩进
        },
      ], // 缩进

      "simple-import-sort/imports": "warn",
      "simple-import-sort/exports": "off",

      "@typescript-eslint/no-deprecated": "warn",
    },
    languageOptions: {
      parser: tseslint.parser,
      parserOptions: {
        projectService: {
          allowDefaultProject: ["eslint.config.ts", "*.mjs"],
        },
        sourceType: "module",
        ecmaVersion: "latest",
      },
      globals: { ...globals.browser },
    },
  },
]
