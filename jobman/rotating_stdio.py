import io
from logging import LogRecord
from logging.handlers import RotatingFileHandler


class RotatingIOWrapper(io.TextIOWrapper):
    def __init__(self, file):
        print("")
        self.fp = RotatingFileHandler(file)

    def write(self, line):
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
