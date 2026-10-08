-- Native editing support for LIP 0.6.3, Neovim 0.10+.
local M = {}
local catalog = require('lip.catalog')
local options = { compiler = 'lipc', indent_width = 4, check_on_save = false }
local checks = {}
local attachments, saving = {}, {}
M.namespace = vim.api.nvim_create_namespace('lip')

-- Delimiter scanning is independent of syntax highlighting.
function M.code(line)
  local result, quoted, escaped = {}, false, false
  for i = 1, #line do
    local char = line:sub(i, i)
    if quoted then
      if escaped then
        escaped = false
      elseif char == '\\' then
        escaped = true
      elseif char == '"' then
        quoted = false
      end
    elseif char == '#' then
      break
    elseif char == '"' then
      result[#result + 1] = '""'
      quoted = true
    else
      result[#result + 1] = char
    end
  end
  return table.concat(result)
end

function M.indent(lnum)
  local stack, previous, previous_indent = {}, '', 0
  local lines = vim.api.nvim_buf_get_lines(0, 0, lnum, false)
  local opening, closing = { ['('] = true, ['{'] = true, ['['] = true },
    { [')'] = true, ['}'] = true, [']'] = true }
  for row = 1, lnum - 1 do
    local code = M.code(lines[row])
    for char in code:gmatch('.') do
      if opening[char] then
        stack[#stack + 1] = vim.fn.indent(row)
      elseif closing[char] then
        stack[#stack] = nil
      end
    end
    if code:match('%S') then
      previous, previous_indent = code, vim.fn.indent(row)
    end
  end
  if M.code(lines[lnum] or ''):match('^%s*[]})]') then
    return stack[#stack] or 0
  end
  if previous:match('=>%s*$') then
    return previous_indent + vim.fn.shiftwidth()
  end
  return #stack == 0 and 0 or stack[#stack] + vim.fn.shiftwidth()
end

function M.complete(findstart, base)
  if findstart == 1 then
    local prefix = vim.api.nvim_get_current_line():sub(1, vim.fn.col('.') - 1)
    return #prefix - #vim.fn.matchstr(prefix, [[\%(\k\|\.\)\+$]])
  end
  local words = {}
  for _, group in ipairs({ catalog.keywords, catalog.types, catalog.builtins }) do
    for _, word in ipairs(group) do
      words[word] = true
    end
  end
  for _, line in ipairs(vim.api.nvim_buf_get_lines(0, 0, -1, false)) do
    local code, offset = M.code(line), 0
    while true do
      local word = vim.fn.matchstr(code, '\\k\\+', offset)
      if word == '' then break end
      offset = vim.fn.matchend(code, '\\k\\+', offset)
      if not word:match('^%d') then words[word] = true end
    end
  end
  local result = {}
  for word in pairs(words) do
    if word:sub(1, #base) == base then result[#result + 1] = word end
  end
  table.sort(result)
  return result
end

function M.highlight(buf)
  vim.api.nvim_buf_call(buf, function()
    vim.cmd('syntax clear')
    vim.cmd('syntax case match')
    local keywords = vim.tbl_filter(function(word)
      return word ~= 'true' and word ~= 'false' and word ~= 'null'
    end, catalog.keywords)
    vim.cmd('syntax keyword lipKeyword ' .. table.concat(keywords, ' '))
    vim.cmd('syntax match lipType /\\<\\%(' .. table.concat(catalog.types, '\\|') .. '\\)\\>/')
    vim.cmd('syntax keyword lipConstant true false null')
    vim.cmd('syntax keyword lipTodo TODO FIXME XXX NOTE contained')
    vim.cmd('syntax match lipComment /#.*/ contains=lipTodo,@Spell')
    vim.cmd([[syntax match lipNumber /\<\d\+\%(\.\d*\)\?\%([eE][+-]\?\d\+\)\?/]])
    local operators = vim.tbl_map(function(word) return vim.fn.escape(word, [=[\/.*^$~[]]=]) end, catalog.operators)
    vim.cmd('syntax match lipOperator /' .. table.concat(operators, '\\|') .. '/')
    vim.cmd([[syntax match lipDelimiter /[(){}\[\],;:.]/]])
    vim.cmd([[syntax match lipFunction /\<\%(flow\|fn\)\s\+\zs\k\+\ze\s*(/]])
    local builtins = vim.tbl_map(function(word) return vim.fn.escape(word, '.') end, catalog.builtins)
    vim.cmd('syntax match lipBuiltin /\\<\\%(' .. table.concat(builtins, '\\|') .. '\\)\\ze\\s*(/')
    vim.cmd([[syntax region lipString start=/"/ skip=/\\./ end=/"/ oneline contains=lipEscape,@Spell]])
    vim.cmd([[syntax match lipEscape /\\\%([abfnrtv"\\]\|x\x\{2}\|u\x\{4}\|U\x\{8}\|[0-7]\{3}\)/ contained]])
    for group, target in pairs({ Keyword = 'Keyword', Type = 'Type', Constant = 'Constant',
      Todo = 'Todo', Comment = 'Comment', Number = 'Number', Operator = 'Operator',
      Delimiter = 'Delimiter', Function = 'Function', Builtin = 'Function',
      String = 'String', Escape = 'SpecialChar' }) do
      vim.api.nvim_set_hl(0, 'lip' .. group, { default = true, link = target })
    end
    vim.b.current_syntax = 'lip'
  end)
end

function M.cancel(buf)
  buf = buf or vim.api.nvim_get_current_buf()
  local request = checks[buf]
  checks[buf] = nil
  if request and request.process then request.process:kill(15) end
end

function M.is_checking(buf)
  return checks[buf or vim.api.nvim_get_current_buf()] ~= nil
end

function M.check(buf, already_saved)
  buf = buf or vim.api.nvim_get_current_buf()
  local file = vim.api.nvim_buf_get_name(buf)
  if file == '' or vim.bo[buf].buftype ~= '' then
    vim.notify('LIP: save this buffer to a .lip file before checking', vim.log.levels.ERROR)
    return
  end
  if vim.fn.executable(options.compiler) ~= 1 then
    vim.notify('LIP: compiler unavailable: ' .. options.compiler, vim.log.levels.ERROR)
    return
  end
  if not already_saved then
    saving[buf] = true
    local saved, err = pcall(vim.api.nvim_buf_call, buf, function() vim.cmd('update') end)
    saving[buf] = nil
    if not saved then
      vim.notify('LIP: could not save file: ' .. tostring(err), vim.log.levels.ERROR)
      return
    end
  end
  M.cancel(buf)
  vim.diagnostic.reset(M.namespace, buf)
  local request = { tick = vim.api.nvim_buf_get_changedtick(buf),
    lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false) }
  checks[buf] = request
  local ok, process = pcall(vim.system, { options.compiler, 'check', '--json', file }, { text = true }, function(result)
    vim.schedule(function()
      if checks[buf] ~= request then return end
      checks[buf] = nil
      if not vim.api.nvim_buf_is_valid(buf) or vim.api.nvim_buf_get_changedtick(buf) ~= request.tick then return end
      local parsed, report = pcall(vim.json.decode, result.stdout or '')
      if not parsed or type(report) ~= 'table' or report.schema ~= 'lip.diagnostics.v1'
        or type(report.diagnostics) ~= 'table' or (result.code ~= 0 and #report.diagnostics == 0) then
        vim.notify('LIP: invalid compiler response: ' .. (result.stderr or result.stdout or ''), vim.log.levels.ERROR)
        return
      end
      local items = {}
      for _, item in ipairs(report.diagnostics) do
        local row = math.max(1, item.line or 1)
        local line = request.lines[row] or ''
        local rune = math.max(0, (item.column or 1) - 1)
        local col, finish = vim.fn.byteidxcomp(line, rune), vim.fn.byteidxcomp(line, rune + 1)
        if col < 0 then col = #line end
        if finish < 0 then finish = #line end
        local message = item.code .. ': ' .. item.message
        if item.hints and #item.hints > 0 then message = message .. '\n' .. table.concat(item.hints, '\n') end
        items[#items + 1] = { lnum = row - 1, col = col, end_lnum = row - 1, end_col = finish,
          severity = vim.diagnostic.severity.ERROR, message = message, source = 'lipc', code = item.code }
      end
      vim.diagnostic.set(M.namespace, buf, items, {})
    end)
  end)
  if ok then
    request.process = process
  else
    checks[buf] = nil
    vim.notify('LIP: could not start compiler: ' .. tostring(process), vim.log.levels.ERROR)
  end
end

function M.attach(buf)
  if not attachments[buf] then
    attachments[buf] = {}
    for _, name in ipairs({ 'commentstring', 'comments', 'expandtab', 'shiftwidth', 'softtabstop',
      'autoindent', 'indentexpr', 'indentkeys', 'omnifunc', 'formatoptions' }) do
      attachments[buf][name] = vim.bo[buf][name]
    end
  end
  vim.bo[buf].commentstring = '# %s'
  vim.bo[buf].comments = 'b:#'
  vim.bo[buf].expandtab = true
  vim.bo[buf].shiftwidth = options.indent_width
  vim.bo[buf].softtabstop = options.indent_width
  vim.bo[buf].autoindent = true
  vim.bo[buf].indentexpr = "v:lua.require'lip'.indent(v:lnum)"
  vim.bo[buf].indentkeys = '0{,0},0),0],!^F,o,O'
  vim.bo[buf].omnifunc = "v:lua.require'lip'.complete"
  local format = vim.bo[buf].formatoptions:gsub('t', '')
  for char in ('cro'):gmatch('.') do
    if not format:find(char, 1, true) then format = format .. char end
  end
  vim.bo[buf].formatoptions = format
  vim.b[buf].undo_ftplugin = "lua require('lip').detach(vim.api.nvim_get_current_buf())"
  vim.api.nvim_buf_create_user_command(buf, 'LipCheck', function() M.check(buf) end,
    { desc = 'Save and check LIP with compiler diagnostics' })
  M.highlight(buf)
end

function M.detach(buf)
  M.cancel(buf)
  vim.diagnostic.reset(M.namespace, buf)
  if attachments[buf] and vim.api.nvim_buf_is_valid(buf) then
    for name, value in pairs(attachments[buf]) do vim.bo[buf][name] = value end
    pcall(vim.api.nvim_buf_del_user_command, buf, 'LipCheck')
    if vim.b[buf].current_syntax == 'lip' then vim.b[buf].current_syntax = nil end
  end
  attachments[buf] = nil
end

function M.setup(config)
  if vim.fn.has('nvim-0.10') ~= 1 then error('LIP requires Neovim 0.10+') end
  options = vim.tbl_extend('force', options, config or {})
  assert(type(options.compiler) == 'string', 'LIP compiler must be an executable name or path')
  assert(type(options.indent_width) == 'number' and options.indent_width > 0
    and options.indent_width % 1 == 0, 'LIP indent_width must be a positive integer')
  vim.filetype.add({ extension = { lip = 'lip' } })
  local group = vim.api.nvim_create_augroup('lip', { clear = true })
  vim.api.nvim_create_autocmd('FileType', { group = group, pattern = 'lip',
    callback = function(event) M.attach(event.buf) end })
  vim.api.nvim_create_autocmd({ 'TextChanged', 'TextChangedI' }, { group = group,
    callback = function(event)
      if vim.bo[event.buf].filetype == 'lip' then
        M.cancel(event.buf)
        vim.diagnostic.reset(M.namespace, event.buf)
      end
    end })
  vim.api.nvim_create_autocmd('BufWipeout', { group = group,
    callback = function(event) M.detach(event.buf) end })
  vim.api.nvim_create_autocmd('BufWritePost', { group = group, pattern = '*.lip',
    callback = function(event)
      if options.check_on_save and not saving[event.buf] and vim.bo[event.buf].filetype == 'lip' then M.check(event.buf, true) end
    end })
  for _, buf in ipairs(vim.api.nvim_list_bufs()) do
    if vim.bo[buf].filetype == 'lip' then M.attach(buf) end
  end
end

return M
