vim9script
import autoload 'lip/catalog.vim'
import autoload 'lip/indent.vim'

export def Complete(findstart: number, base: string): any
  if findstart
    var start = col('.') - 1
    var line = getline('.')
    while start > 0 && strpart(line, start - 1, 1) =~ '[[:alnum:]_.\x80-\xff]'
      start -= 1
    endwhile
    return start
  endif
  var words = catalog.Keywords() + catalog.Types() + catalog.Builtins()
  for line in getline(1, '$')
    var code = indent.Code(line)
    var offset = 0
    while true
      var word = matchstr(code, '\k\+', offset)
      if empty(word)
        break
      endif
      offset = matchend(code, '\k\+', offset)
      if word !~ '^\d'
        add(words, word)
      endif
    endwhile
  endfor
  return filter(uniq(sort(words)), (_, word) => stridx(word, base) == 0)
enddef

export def Check()
  var file = expand('%:p')
  if empty(file) || &buftype != ''
    echoerr 'LIP: save this buffer to a .lip file before checking'
    return
  endif
  var compiler = get(g:, 'lipc_command', 'lipc')
  if !executable(compiler)
    echoerr 'LIP: lipc was not found; set g:lipc_command to its executable path'
    return
  endif
  update
  var output = system(shellescape(compiler) .. ' check --json ' .. shellescape(file))
  var status = v:shell_error
  try
    var report = json_decode(output)
    if get(report, 'schema', '') != 'lip.diagnostics.v1'
      throw 'unexpected diagnostic schema'
    endif
    var items: list<dict<any>> = []
    for diagnostic in report.diagnostics
      var line = max([1, get(diagnostic, 'line', 1)])
      var column = max([1, get(diagnostic, 'column', 1)])
      # Compiler columns count Unicode runes, including combining marks.
      var byte = byteidxcomp(getline(line), column - 1)
      column = byte < 0 ? strlen(getline(line)) + 1 : byte + 1
      var message = diagnostic.code .. ': ' .. diagnostic.message
      if !empty(get(diagnostic, 'hints', []))
        message ..= ' | ' .. join(diagnostic.hints, ' | ')
      endif
      add(items, {filename: file, lnum: line, col: column, type: 'E', text: message})
    endfor
    if status != 0 && empty(items)
      throw 'compiler failed without diagnostics'
    endif
    setqflist([], 'r', {title: 'LIP check: ' .. file, items: items})
    if empty(items)
      cclose
      echom 'LIP: check passed'
    else
      copen
    endif
  catch
    echoerr 'LIP: invalid compiler response: ' .. v:exception .. ' ' .. output
  endtry
enddef
