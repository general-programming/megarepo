import re


class BaseCommand:
    MATCH_REGEX = r""

    def matches(self, cmd: str):
        if re.match(self.MATCH_REGEX, cmd):
            return True

    async def run(self, cmd: str):
        raise NotImplementedError

    @staticmethod
    def prompt(prompt: str, expected_response: str):
        response = input(prompt).strip().lower()

        return response == expected_response.lower()
