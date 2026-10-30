"""CAP08 fixed lifecycle adapter; no load is started implicitly.

The reviewed base keeps CID/project/port/ACL/build identity and exact cleanup
guards. CAP08 has a distinct runtime, observer, output root and active lock.
"""
import importlib.util
from pathlib import Path


spec = importlib.util.spec_from_file_location('cap08_owned_environment', Path(__file__).with_name('cap06-env.py'))
owned = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)
owned.configure_cohort('cap08')


if __name__ == '__main__':
    try:
        owned.main()
    except Exception as error:
        print('cap08-env: ' + type(error).__name__ + '; details suppressed; preserve public progress and private recovery files')
        raise SystemExit(1)
