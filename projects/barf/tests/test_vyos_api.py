from barf.util.vyos_api import SystemImage, parse_system_images

TABLE_OUTPUT = """
Name                       Default boot    Running
--------------------------------------------------
2026.06.30-0048-rolling    Yes             Yes
2026.03.28-0028-rolling
y                          Yes
"""

LEGACY_OUTPUT = """
The system currently has the following image(s) installed:

   1: 2026.06.30-0048-rolling (default boot) (running image)
   2: 2026.03.28-0028-rolling
"""


def test_parse_table_format():
    assert parse_system_images(TABLE_OUTPUT) == [
        SystemImage("2026.06.30-0048-rolling", default_boot=True, running=True),
        SystemImage("2026.03.28-0028-rolling"),
        SystemImage("y", default_boot=True),
    ]


def test_parse_legacy_format():
    assert parse_system_images(LEGACY_OUTPUT) == [
        SystemImage("2026.06.30-0048-rolling", default_boot=True, running=True),
        SystemImage("2026.03.28-0028-rolling"),
    ]


def test_parse_empty():
    assert parse_system_images("") == []
