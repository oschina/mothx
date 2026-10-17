import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "sync-pypi-version.sh"


@unittest.skipIf(shutil.which("bash") is None, "bash is required for version sync tests")
class PyPIVersionSyncTests(unittest.TestCase):
    def sync(self, version):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            scripts = root / "scripts"
            scripts.mkdir()
            shutil.copy2(SCRIPT, scripts / SCRIPT.name)
            pypi = root / "pypi"
            (pypi / "src" / "mothx_installer").mkdir(parents=True)
            (pypi / "pyproject.toml").write_text('[project]\nname = "mothx-installer"\nversion = "0.0.0"\n')
            init_file = pypi / "src" / "mothx_installer" / "__init__.py"
            init_file.write_text('__version__ = "0.0.0"\n')

            env = dict(os.environ, PYTHON=sys.executable)
            subprocess.run(["bash", str(scripts / SCRIPT.name), version], check=True, env=env, capture_output=True)

            pyproject = (pypi / "pyproject.toml").read_text()
            init = init_file.read_text()
            self.assertIn('version = "', pyproject)
            self.assertIn('__version__ = "', init)
            return pyproject.split('version = "', 1)[1].split('"', 1)[0], init.split('__version__ = "', 1)[1].split('"', 1)[0]

    def test_numeric_pre_release_maps_to_matching_rc(self):
        self.assertEqual(self.sync("v1.3.105-pre-2"), ("1.3.105rc2", "1.3.105rc2"))

    def test_bare_pre_release_gets_rc1(self):
        self.assertEqual(self.sync("1.3.105-pre"), ("1.3.105rc1", "1.3.105rc1"))

    def test_stable_version_is_unchanged(self):
        self.assertEqual(self.sync("v1.3.105"), ("1.3.105", "1.3.105"))

    def test_numeric_alpha_and_beta_keep_their_ordinal(self):
        self.assertEqual(self.sync("1.3.105-alpha-3"), ("1.3.105a3", "1.3.105a3"))
        self.assertEqual(self.sync("1.3.105-beta-4"), ("1.3.105b4", "1.3.105b4"))


if __name__ == "__main__":
    unittest.main()
