class JobmanError(Exception):
    pass


class JobmanForkError(JobmanError):
    def __init__(self, message):
        super().__init__(f"Failed to fork jobman process: {message}")
