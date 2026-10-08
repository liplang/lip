vim9script
# LIP 0.6.3. Only # introduces comments.
import autoload 'lip/catalog.vim'
if exists('b:current_syntax')
  finish
endif
syntax case match
execute 'syntax keyword lipKeyword ' .. join(filter(catalog.Keywords(), (_, word) => index(['true', 'false', 'null'], word) < 0))
execute 'syntax match lipType /\<\%(' .. join(catalog.Types(), '\|') .. '\)\>/'
syntax keyword lipConstant true false null
syntax keyword lipTodo TODO FIXME XXX NOTE contained
syntax match lipComment /#.*/ contains=lipTodo,@Spell
syntax match lipNumber /\<\d\+\%(\.\d*\)\?\%([eE][+-]\?\d\+\)\?/
execute 'syntax match lipOperator /' .. join(map(catalog.Operators(), (_, word) => escape(word, '\/.*^$~[]')), '\|') .. '/'
syntax match lipDelimiter /[(){}\[\],;:.]/
syntax match lipFunction /\<\%(flow\|fn\)\s\+\zs\k\+\ze\s*(/
execute 'syntax match lipBuiltin /\<\%(' .. join(map(catalog.Builtins(), (_, word) => escape(word, '.')), '\|') .. '\)\ze\s*(/'
syntax region lipString start=/"/ skip=/\\./ end=/"/ oneline contains=lipEscape,@Spell
syntax match lipEscape /\\\%([abfnrtv"\\]\|x\x\{2}\|u\x\{4}\|U\x\{8}\|[0-7]\{3}\)/ contained
highlight default link lipKeyword Keyword
highlight default link lipType Type
highlight default link lipConstant Constant
highlight default link lipTodo Todo
highlight default link lipComment Comment
highlight default link lipNumber Number
highlight default link lipOperator Operator
highlight default link lipDelimiter Delimiter
highlight default link lipFunction Function
highlight default link lipBuiltin Function
highlight default link lipString String
highlight default link lipEscape SpecialChar
b:current_syntax = 'lip'
