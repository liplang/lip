"""Exercise chat editing on a real PTY, including timer output during input."""

import fcntl
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time


master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 0, 0))
original = termios.tcgetattr(slave)
env = dict(os.environ, TERM="xterm-256color", PROMPT_TOOLKIT_NO_CPR="1")
process = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=slave, env=env)
buffer = b""


def wait(token):
    global buffer
    token = token.encode() if isinstance(token, str) else token
    deadline = time.monotonic() + 15
    while token not in buffer:
        if time.monotonic() > deadline or process.poll() is not None:
            raise AssertionError("waiting for %r; received %r" % (token, buffer[-4000:]))
        if select.select([master], [], [], 0.05)[0]:
            buffer += os.read(master, 65536)
    buffer = buffer[buffer.index(token) + len(token):]


def ready():
    wait(b"\x1b[?2004h")
    assert not (termios.tcgetattr(slave)[3] & termios.ICANON)


def send(text):
    os.write(master, text.encode())


def value(keys, expected):
    send(keys + "\r")
    wait("助手> 收到输入：" + expected)
    ready()


try:
    ready()
    value("echo: 你错\x08好", "你好")           # Ctrl-H, Unicode
    value("echo: 你错\x7f好", "你好")           # DEL, Unicode
    value("echo: ac\x1b[Db", "abc")             # left / insertion
    value("echo: abxc\x1b[D\x1b[D\x1b[3~", "abc")  # Delete
    value("草稿\x01\x0b" + "echo: alpha", "alpha")  # Home / kill
    value("echo: alph\x01\x05" + "a", "alpha")  # End
    value("\x1b[A", "alpha")                  # history
    value("\x12你好\r", "你好")                # reverse search, then submit
    value("\x1b[200~echo: 第一行\n第二行\x1b[201~", "第一行\r\n第二行")

    send("edit-reminder\r")
    wait("助手> 提醒已设置")
    ready()
    send("echo: 保留")
    wait("echo: 保留")
    with open(os.environ["LIP_CHAT_EDIT_GATE"], "w"):
        pass
    wait("编辑时主动提醒")
    value("草稿", "保留草稿")                    # notification preserves draft

    send("echo: 不应提交\x03")
    ready()                                     # Ctrl-C clears, process survives
    send("\x04")                              # EOF exits cleanly
    assert process.wait(timeout=10) == 0
    assert termios.tcgetattr(slave) == original, "terminal settings not restored"

    # Redirected stdin must stay a pipe even when stdout is a terminal.
    process = subprocess.Popen(sys.argv[1:], stdin=subprocess.PIPE, stdout=slave, stderr=slave, env=env)
    assert process.communicate(b"/time\n/exit\n", timeout=15) == (None, None)
    assert process.returncode == 0
    assert termios.tcgetattr(slave) == original
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
