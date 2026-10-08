set encoding=utf-8
execute 'set runtimepath^=' . fnameescape($LIP_EDITOR_VIM)
filetype plugin indent on
syntax enable
let g:lipc_command = $LIP_TEST_COMPILER
execute 'edit ' . fnameescape($LIP_EDITOR_EXAMPLE)
call assert_equal('lip', &filetype)
call assert_equal('lip', b:current_syntax)
call assert_equal('# %s', &commentstring)
call assert_equal('lip#Complete', &omnifunc)
call assert_equal(4, &shiftwidth)

function! Group(line, word) abort
  let column = stridx(getline(a:line), a:word) + 1
  call assert_true(column > 0, a:word)
  return synIDattr(synID(a:line, column, 1), 'name')
endfunction
call assert_equal('lipKeyword', Group(6, 'match'))
call assert_equal('lipKeyword', Group(10, 'continue'))
call assert_equal('lipKeyword', Group(11, 'break'))
call assert_equal('lipOperator', Group(19, '//'))
call assert_equal('lipOperator', Group(20, '*/'))
call assert_equal('lipOperator', Group(21, '**'))
call assert_equal('lipNumber', Group(23, '1.25e-2'))
call assert_equal('lipComment', Group(4, '#'))
call assert_equal('lipString', Group(12, '#'))
call assert_equal('lipString', Group(12, '//'))
call assert_equal('lipString', Group(12, '{}'))
call assert_equal('lipString', Group(12, 'list.map'))
call assert_equal('lipComment', Group(4, 'list.map'))
call assert_equal('lipBuiltin', Group(24, 'list.map'))
call assert_equal('lipBuiltin', Group(25, 'string.trim'))
call assert_notequal('lipKeyword', Group(5, 'when'))
call assert_equal(['list.map', 'list.max'], lip#Complete(0, 'list.ma'))
call assert_true(index(lip#Complete(0, '双'), '双倍') >= 0)

" Indentation must work with syntax disabled and be idempotent.
let expected = getline(1, '$')
syntax off
call setline(1, map(copy(expected), 'substitute(v:val, "^ *", "", "")'))
normal! gg=G
call assert_equal(expected, getline(1, '$'))
normal! gg=G
call assert_equal(expected, getline(1, '$'))

execute 'edit! ' . fnameescape($LIP_EDITOR_BAD)
LipCheck
let errors = getqflist()
call assert_equal(1, len(errors))
if len(errors) == 1
  let wanted = json_decode(join(readfile($LIP_EDITOR_EXPECTED), ''))
  call assert_equal(wanted.line, errors[0].lnum)
  call assert_equal(wanted.byte_column, errors[0].col)
  call assert_equal(fnamemodify($LIP_EDITOR_BAD, ':p'), fnamemodify(bufname(errors[0].bufnr), ':p'))
  call assert_match('LIP_', errors[0].text)
endif
wincmd p
call setline(1, ['flow Fixed() -> number { return 7 }'])
if line('$') > 1
  2,$delete _
endif
LipCheck
call assert_equal([], getqflist())

enew!
setfiletype lip
set virtualedit=onemore
call setline(1, '变量 = 双倍')
call cursor(1, strlen(getline(1)) + 1)
call assert_equal(strlen('变量 = '), lip#Complete(1, ''))

if !empty(v:errors)
  call writefile(v:errors, $LIP_EDITOR_FAILURES)
  cquit
endif
qa!
