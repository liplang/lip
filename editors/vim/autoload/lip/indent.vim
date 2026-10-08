vim9script
# Strip comments and string contents independently of syntax highlighting.
export def Code(text: string): string
  var out = ''
  var quoted = false
  var escaped = false
  for char in split(text, '\zs')
    if quoted
      if escaped
        escaped = false
      elseif char == '\'
        escaped = true
      elseif char == '"'
        quoted = false
      endif
    elseif char == '#'
      break
    elseif char == '"'
      out ..= '""'
      quoted = true
    else
      out ..= char
    endif
  endfor
  return out
enddef

export def Get(lnum: number): number
  var stack: list<number> = []
  var previous = ''
  var previous_indent = 0
  for line in range(1, lnum - 1)
    var code = Code(getline(line))
    for char in split(code, '\zs')
      if char =~ '[({\[]'
        add(stack, indent(line))
      elseif char =~ '[)}\]]' && !empty(stack)
        remove(stack, -1)
      endif
    endfor
    if code =~ '\S'
      previous = code
      previous_indent = indent(line)
    endif
  endfor
  var current = Code(getline(lnum))
  if current =~ '^\s*[)}\]]'
    return empty(stack) ? 0 : stack[-1]
  endif
  if previous =~ '=>\s*$'
    return previous_indent + shiftwidth()
  endif
  return empty(stack) ? 0 : stack[-1] + shiftwidth()
enddef
