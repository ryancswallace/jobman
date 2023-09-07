from jobman.jobman import get_host_id

def test_get_host_id():
    host_id = get_host_id()
    assert len(host_id) == 12