# LIP 0.6.4 编辑器支持

发行包的 `editors/` 提供三套原生插件：Neovim 使用 Lua（0.10+），Vim 使用
Vim9script（Vim 9.0+，需带 `+vim9script`），Emacs 使用 Elisp（27.1+）。
安装只需复制对应子目录，不需要 Go；编译器检查需要可执行的 `lipc`。
以下安装命令从仓库或发行包根目录运行；插件不会自动修改个人配置。

| 功能 | Neovim | Vim | Emacs |
| --- | --- | --- | --- |
| `.lip` 自动识别、高亮 | Lua + 原生 syntax | Vim9script syntax | lip-mode + font-lock |
| 缩进 | 默认 4 空格，`gg=G` | 默认 4 空格，`gg=G` | `TAB` / `indent-region` |
| 补全 | omnifunc，插入模式 `Ctrl-X Ctrl-O` | omnifunc，插入模式 `Ctrl-X Ctrl-O` | `completion-at-point`，通常 `M-TAB` |
| 检查 | `:LipCheck`，保存后异步检查 | `:LipCheck`，保存后同步检查 | `C-c C-c` / `M-x lip-check` 保存检查 |
| 定位错误 | 原生 `vim.diagnostic` | quickfix，`:cnext` / `:cprev` | Flymake 诊断列表与跳转 |
| 自动检查 | 可选保存后检查 | 手动检查 | 默认 Flymake 检查未保存文本 |

高亮覆盖当前关键字、类型、内置调用、数字、字符串/转义和 `#` 行注释。
`//` 是向下取整除法，`*/` 是对数，`**` 是乘方；它们均不会被识别为注释。
`when` 是普通标识符。缩进处理嵌套括号与 `=>` 后换行，并忽略字符串和注释。
补全来自语言词库与当前文件标识符，支持中文名称；它按拼写提供建议，不解析
作用域、导入别名或宿主注册信息。编译器检查才负责名称、类型和依赖的语义验证。
目前提供轻量编辑支持，LSP、Tree-sitter、项目导航和独立格式化器仍待实现。

## Neovim：Lua

使用原生 package。Linux/macOS 的常见数据目录安装方式：

```sh
mkdir -p ~/.local/share/nvim/site/pack/lip/start/lip
cp -R editors/neovim/. ~/.local/share/nvim/site/pack/lip/start/lip/
```

实际数据目录可在 Neovim 中用 `:lua print(vim.fn.stdpath('data'))` 查看；自定义
XDG 路径或 Windows 用户应将插件复制到该目录的 `site/pack/lip/start/lip/`。
默认即可识别 `.lip` 并从 PATH 调用 `lipc`。可在 `init.lua` 中配置：

```lua
vim.cmd('packadd lip')
require('lip').setup({
  compiler = vim.fn.expand('~/go/bin/lipc'),
  indent_width = 4,
  check_on_save = false, -- 改为 true 启用保存后检查
})
```

也可以通过插件管理器将 `editors/neovim` 加入 runtimepath，再调用同一 `setup`。
`compiler` 是单个可执行文件名或完整路径，不是带参数的 shell 命令。
`:LipCheck` 将结果显示为原生诊断，可用：

```lua
vim.diagnostic.open_float()
vim.diagnostic.setloclist()
```

检查通过会清除旧诊断；修改文本、关闭 buffer 或发起新检查会取消/丢弃旧检查。
编译器调用使用参数数组，支持带空格的路径，不阻塞编辑。插件的入口、缩进、
补全、诊断和 syntax 加载均由 Lua 实现，无需加载 Vim 插件。

## Vim：Vim9script

Linux/macOS 的原生 package 安装方式：

```sh
mkdir -p ~/.vim/pack/lip/start/lip
cp -R editors/vim/. ~/.vim/pack/lip/start/lip/
```

Windows 通常使用 `~/vimfiles/pack/lip/start/lip/`；可用 `:set packpath?` 查看。
确保 vimrc 中启用了 `filetype plugin indent on` 与 `syntax enable`。
可采用下面这份最小 Vim9 配置（`vim9script` 必须是文件第一条命令）：

```vim
vim9script
filetype plugin indent on
syntax enable
g:lipc_command = expand('~/go/bin/lipc')
g:lip_indent_width = 4
```

已有旧式 vimrc 可以继续使用，只需把两项赋值写成
`let g:lipc_command = ...`、`let g:lip_indent_width = 4`；插件本身仍全部采用 Vim9script。
`g:lipc_command` 默认 `lipc`，只接受可执行文件名或完整路径。
`:LipCheck` 更新文件并用 JSON 诊断替换 quickfix；通过时关闭 quickfix 窗口。
调用会等待检查结束，耗时取决于文件大小。不要将这个 Vim9 插件安装到 Neovim。

## Emacs：Elisp

```sh
mkdir -p ~/.emacs.d/lip
cp -R editors/emacs/. ~/.emacs.d/lip/
```

在 Emacs 配置中加入：

```elisp
(add-to-list 'load-path (expand-file-name "~/.emacs.d/lip"))
(require 'lip-mode)
(setq lipc-command (expand-file-name "~/go/bin/lipc"))
(setq lip-indent-offset 4)
;; 如需仅手动检查：
;; (setq lip-enable-flymake nil)
```

重新打开 `.lip` 文件即可启用 `lip-mode`。找到编译器时默认启用 Flymake，
异步检查当前 buffer 的 UTF-8 临时副本，因此未保存修改也会被检查。
`M-x flymake-show-buffer-diagnostics` 显示列表（较早 Emacs 使用
`flymake-show-diagnostics-buffer`），`flymake-goto-next-error` 跳转。
检查取代旧进程，修改后的过期结果不会覆盖当前诊断；检查结束、取消或关闭 buffer
会清理临时文件和输出 buffer。`C-c C-c` 保存文件并发起检查。
若配置在文件打开之后发生变化，可手动 `M-x flymake-mode` 启用背景检查。

## 编译器与定位契约

三套插件都调用 `lipc check --json`，读取 `lip.diagnostics.v1`；检查不执行程序，
不启动 Host/Go adapter 或 Python Worker，也不需要 Go 工具链。当前编译器报告
首个错误及修复提示；插件不宣称一次发现所有错误。
编译器列号以 Unicode 字符计数，包括组合字符，Tab 计一个字符。
Vim/Neovim 会换算为 UTF-8 字节列，Emacs 换算为字符位置，避免中文后的光标错位。
编译器路径错误或非 JSON 响应会显示失败，不能当成检查通过。

## 维护与验证

关键字/运算符来自真实 lexer，list/string 调用名来自共用标准库目录，版本来自
Runtime；核心操作与类型拼写集中在生成器。生成 JSON、Vim9、Lua 和 Elisp 数据：

```sh
go run ./scripts/editor-catalog
go run ./scripts/editor-catalog --check
python3 scripts/verify-editors.py --require-all
```

验证脚本将插件复制到仓库外带空格的路径，在真实 Vim、Neovim、Emacs 中检查
文件识别、高亮、缩进幂等性、Unicode 补全和真实编译器诊断；另验证异步过期结果、
取消与资源清理。可用 `--compiler /path/to/lipc` 复用已构建的编译器。
Emacs 模式在临时安装目录字节编译，编译警告视为失败，再运行原生测试。
不带 `--require-all` 时明确跳过未安装的编辑器；三者都缺失会失败。
`bash scripts/verify-release.sh` 包含词库一致性和可用编辑器的测试；
三者都缺失时主脚本明确跳过原生测试。
