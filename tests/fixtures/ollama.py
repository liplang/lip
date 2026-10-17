"""Offline SDK double, enabled only by the chat integration test's PYTHONPATH."""

import gc
import json
import os
import sys
import weakref


responses = weakref.WeakSet()


class Chunk:
    def __init__(self, message, done):
        self.message = message
        self.done = done
        responses.add(self)

    def model_dump(self, mode):
        assert mode == "json"
        return {"message": self.message, "done": self.done}


class RequestProbe:
    """A replaceable streaming endpoint with observable call/close events."""

    def __init__(self):
        self.events = []

    def chat(self, messages, tools):
        assert tools == []
        text = messages[-1]["content"]
        self.events.append("request:" + text)

        def chunks():
            try:
                yield Chunk({"content": "", "thinking": text, "tool_calls": []}, False)
                yield Chunk({"content": None, "thinking": None, "tool_calls": None}, True)
            finally:
                self.events.append("close")

        return chunks()


class Client:
    def __init__(self, host, timeout):
        pass

    def close(self):
        pass

    def chat(self, model, messages, tools, stream, keep_alive, options):
        assert model == (os.environ.get("LIP_CHAT_MODEL") or "llama3.1:8b")
        assert keep_alive == "30m"
        assert options == {"temperature": 0.2, "num_predict": 1024}
        if sys.stdout.isatty():
            import termios
            assert termios.tcgetattr(sys.stdout)[3] & termios.ICANON, "raw mode leaked into model execution"
        gc.collect()
        assert not responses, "stream chunks retained from an earlier request"
        assert stream
        assert "本轮当前时间：" in messages[0]["content"]
        current_time = json.loads(messages[0]["content"].split("本轮当前时间：")[-1])
        assert "+08:00" in current_time["value"]
        assert "UTC+0800" in current_time["display"]
        assert len([m for m in messages if m["role"] == "user"]) <= 12
        assert sum(len(m.get("content", "")) for m in messages[1:]) <= 24000
        # Trimming must preserve every assistant/tool group, in order.
        pending = []
        for message in messages[1:]:
            if message["role"] == "tool":
                assert pending and pending.pop(0) == message["tool_name"]
            else:
                assert not pending, "orphaned tool calls after trimming"
                pending = [call["function"]["name"] for call in message.get("tool_calls", [])]
        assert not pending
        last_user = max(i for i, message in enumerate(messages) if message["role"] == "user")
        prompt = messages[last_user]["content"]
        meta = prompt in ("你是什么助手 agent？", "编辑体验太差，响应速度太慢了")
        assert {tool["function"]["name"] for tool in tools} == (set() if meta else {
            "calculate", "get_current_time", "shift_time", "list_calculations",
            "set_reminder", "list_reminders", "cancel_reminder",
        })
        observations = [json.loads(m["content"]) for m in messages[last_user + 1:] if m["role"] == "tool"]
        calls = []

        def call(name, **args):
            calls.append({"function": {"name": name, "arguments": args}})

        content = "验证完成"
        if prompt == "setup" and not observations:
            call("calculate", operation="multiply", a=123, b=456, label="采购预算")
            call("get_current_time")
            call("calculate", operation="divide", a=1, b=0)
            call("list_calculations", query="采购")
            call("set_reminder", message="取消的提醒", after_seconds=3600)
            call("list_reminders")
            call("cancel_reminder", id="1")
            call("list_reminders", status="all")
            call("unknown_tool")
        elif prompt == "setup":
            assert len(observations) == 9
            assert observations[0]["saved"] and observations[0]["value"] == 56088
            assert observations[1]["ok"]
            assert not observations[2]["ok"]
            assert observations[3]["calculations"][0]["label"] == "采购预算"
            assert observations[4]["id"] == "1"
            assert observations[5]["reminders"][0]["status"] == "pending"
            assert observations[6]["status"] == "cancelled"
            assert observations[7]["reminders"][0]["status"] == "cancelled"
            assert not observations[8]["ok"]
        elif prompt == "read" and not observations:
            call("list_calculations", query="采购")
            call("list_reminders", status="all")
        elif prompt == "read":
            assert observations[0]["calculations"][0]["value"] == 56088
            assert observations[1]["reminders"][0]["status"] == "cancelled"
            content = "跨重启记录已验证"
        elif prompt == "idle" and not observations:
            call("set_reminder", message="空闲时主动提醒", after_seconds=0.1)
        elif prompt == "resume" and not observations:
            call("set_reminder", message="重启后补发", after_seconds=3600)
        elif prompt == "loop":
            call("get_current_time")
        elif prompt == "chat":
            content = "普通聊天"
        elif prompt == "fresh":
            assert len([m for m in messages if m["role"] == "user"]) == 1
        elif prompt == "long":
            content = "长对话" * 4400
        elif prompt == "现在几点了":
            content = current_time["display"]
        elif prompt == "numeric-strings" and not observations:
            call("calculate", operation="add", a="60", b="52")
        elif prompt == "numeric-strings":
            assert observations[0]["ok"] and observations[0]["value"] == 112
            content = "数字参数兼容已验证"
        elif prompt.startswith("echo: "):
            content = "收到输入：" + prompt[6:]
        elif prompt == "edit-reminder" and not observations:
            call("set_reminder", message="编辑时主动提醒", after_seconds=1)
        elif prompt == "edit-reminder":
            content = "提醒已设置"
        elif prompt == "从现在再过31小时52分钟" and not observations:
            call("shift_time", hours=31, minutes=52)
        elif prompt == "从现在再过31小时52分钟":
            assert observations[0]["offset_seconds"] == 114720
            content = observations[0]["display"]
        elif meta and not observations:
            # A hallucinated call must never write to the database when the
            # current message has no tools, even if the SDK returns one.
            call("set_reminder", message="不应保存的提醒", after_seconds=3600)
        elif meta:
            assert len(observations) == 1 and not observations[0]["ok"]
            content = "我是本地 Ollama 助手，已收到使用体验反馈"
        elif prompt not in ("idle", "resume"):
            raise AssertionError("unexpected prompt: " + prompt)
        yield Chunk({"content": "", "thinking": "", "tool_calls": calls}, False)
        gc.collect()
        assert not responses, "a converted stream chunk was not released"
        yield Chunk({"content": "" if calls else content, "thinking": None, "tool_calls": None}, True)
