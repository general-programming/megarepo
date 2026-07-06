"""Offline pieces of the Safe-Mode session (barf/vendors/mikrotik/ros_ssh.py).

The session choreography itself (take, apply, release / drop-revert)
is hardware-coupled and proven by a live spike against sea420 before
first use; these cover the parsing that decides success vs. abort.
"""

from barf.vendors.mikrotik.ros_ssh import PROMPT_RE, clean_terminal, looks_like_error


class TestPromptDetection:
    def test_plain_prompt(self):
        assert PROMPT_RE.search("[barf@sea420-acc-v-hv2] > ")

    def test_safe_mode_prompt(self):
        assert PROMPT_RE.search("[barf@sea420-acc-v-hv2] <SAFE> ")
        assert PROMPT_RE.search("[barf@sea420-acc-v-hv2] <SAFE>> ")

    def test_output_lines_are_not_prompts(self):
        assert not PROMPT_RE.search("  progress: done")
        assert not PROMPT_RE.search("Columns: NAME, PORT")

    def test_real_722_safe_mode_transcript(self):
        # Verbatim from sea420 (7.22.3): cursor moves and a terminal
        # attributes query ride along even on a +ct dumb terminal.
        raw = (
            "\r[barf@sea420-acc-v-hv2] > \r\n"
            "Taking Safe Mode session... Success!\r\n"
            "\r\x1b[9999B\r\r\r\r\x1b[9999B[barf@sea420-acc-v-hv2] <SAFE> \x1b[c"
        )
        cleaned = clean_terminal(raw)
        last = cleaned.rsplit("\n", 1)[-1]
        assert PROMPT_RE.search(last)
        assert "<SAFE>" in last


class TestErrorDetection:
    def test_failure_lines(self):
        assert looks_like_error("failure: CA not found")
        assert looks_like_error("syntax error (line 1 column 9)")
        assert looks_like_error("bad command name wireguard")
        assert looks_like_error("input does not match any value of certificate")

    def test_clean_output_passes(self):
        assert looks_like_error("Flags: X - disabled\n 0 X www-ssl 443") is None
        assert looks_like_error("") is None

    def test_bare_bracketed_token_is_not_a_prompt(self):
        # Command output can end a chunk with "[user@host]" (e.g. log
        # lines); only <SAFE> or a trailing ">" marks a real prompt.
        assert not PROMPT_RE.search("[barf@sea420-acc-v-hv2]")
        assert not PROMPT_RE.search("session opened for [admin@10.3.0.1] ")
