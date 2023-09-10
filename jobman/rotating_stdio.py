import io
from logging import LogRecord
from logging.handlers import RotatingFileHandler
from pathlib import Path

class RotatingIOWrapper(io.TextIOWrapper):
    def __init__(self, file: Path):
        print("")
        self.fp = RotatingFileHandler(file)

    def write(self, line: str) -> int:
        print("WRITING")
        record = LogRecord(
            name="",
            level=1,
            pathname="jobman",
            lineno=0,
            msg=line,
            args=tuple(),
            exc_info=None,
        )
        self.fp.emit(record)
        return 0
