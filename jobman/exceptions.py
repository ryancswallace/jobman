class JobmanError(Exception):
    def __init__(self, message, exit_code):
        super().__init__(message)
        self.exit_code = exit_code


class JobmanForkError(JobmanError):
    pass
