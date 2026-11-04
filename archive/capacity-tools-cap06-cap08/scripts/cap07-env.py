"""CAP07 lifecycle adapter over the already reviewed fixed CAP-read guards.

Only the cohort root/runtime/observer bookkeeping changes. The project, ports,
container/PID identities, private ACLs, normal logout and retained volumes keep
the same implementation. This adapter never starts load implicitly.
"""
import importlib.util
from pathlib import Path


spec = importlib.util.spec_from_file_location('capacity_owned_environment', Path(__file__).with_name('cap06-env.py'))
owned = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)
owned.configure_cohort('cap07')


if __name__ == '__main__':
    try:
        owned.main()
    except Exception as error:
        print('cap07-env: ' + type(error).__name__ + '; details suppressed; preserve public progress and private recovery files')
        raise SystemExit(1)
