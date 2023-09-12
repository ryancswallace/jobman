import hashlib
import platform


def get_host_id() -> str:
    system_info = platform.uname()
    system_info_str = ";".join(
        [
            system_info.node,
            system_info.system,
            system_info.release,
            system_info.version,
            system_info.machine,
            system_info.processor,
        ]
    )
    host_id = hashlib.sha256(system_info_str.encode()).hexdigest()[:12]
    return host_id
