"""Copy installed dependency notices beside the native protocol helper."""
import importlib.metadata
from pathlib import Path
import sys

target = Path(sys.argv[1]) / 'licenses'
target.mkdir(parents=True, exist_ok=True)
for name in ('curl_cffi', 'cffi', 'pycparser', 'certifi', 'pyinstaller'):
    distribution = importlib.metadata.distribution(name)
    notices = [item for item in distribution.files or []
               if '/licenses/' in str(item).lower()]
    if not notices:
        raise RuntimeError(f'Missing installed license for {name}')
    for item in notices:
        source = Path(distribution.locate_file(item))
        (target / f'{name}-{source.name}').write_bytes(source.read_bytes())
license_file = Path(sys.base_prefix) / 'LICENSE.txt'
if license_file.is_file():
    python_notice = license_file.read_text('utf-8')
else:
    # Python's embedded license is available even on distributions omitting LICENSE.txt.
    printer = __import__('builtins').license
    printer._Printer__setup()
    python_notice = '\n'.join(printer._Printer__lines)
(target / 'Python-LICENSE.txt').write_text(python_notice, encoding='utf-8')
print('Protocol helper dependency notices copied.')
