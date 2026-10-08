vim9script
import autoload 'lip.vim'
if exists('b:did_ftplugin')
  finish
endif
b:did_ftplugin = 1
setlocal commentstring=#\ %s comments=b:#
setlocal formatoptions-=t formatoptions+=cro
setlocal omnifunc=lip#Complete
command! -buffer LipCheck lip.Check()
b:undo_ftplugin = 'setlocal commentstring< comments< formatoptions< omnifunc< | silent! delcommand LipCheck | unlet! b:did_ftplugin'
