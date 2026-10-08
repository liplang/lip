vim9script
if exists('b:did_indent')
  finish
endif
b:did_indent = 1
&l:shiftwidth = get(g:, 'lip_indent_width', 4)
&l:softtabstop = &l:shiftwidth
setlocal expandtab autoindent
setlocal indentexpr=lip#indent#Get(v:lnum)
setlocal indentkeys=0{,0},0),0],!^F,o,O
b:undo_indent = 'setlocal shiftwidth< softtabstop< expandtab< autoindent< indentexpr< indentkeys< | unlet! b:did_indent'
