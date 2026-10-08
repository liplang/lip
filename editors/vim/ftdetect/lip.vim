vim9script
augroup lip_filetype
  autocmd!
  autocmd BufRead,BufNewFile *.lip setfiletype lip
augroup END
