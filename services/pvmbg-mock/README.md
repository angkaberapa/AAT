# PVMBG Mock Service

FastAPI scaffold for the IF4031 Milestone 1 PVMBG mock.

## Local development

Python 3.11 or newer is required. Run these commands from the repository root.

### PowerShell (Windows)

```powershell
cd .\services\pvmbg-mock
python -m venv .venv

.\.venv\Scripts\Activate.ps1

python -m pip install --upgrade pip
python -m pip install -e ".[dev]"

$env:PVMBG_TOKEN = "pvmbg.v1.change-me"
uvicorn app.main:app --host 0.0.0.0 --port 8082 --reload
```

If activation is blocked by PowerShell, enable scripts for the current shell only:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
.\.venv\Scripts\Activate.ps1
```

### Bash (Linux/macOS)

```bash
cd services/pvmbg-mock
python3 -m venv .venv

source .venv/bin/activate

python -m pip install --upgrade pip
python -m pip install -e ".[dev]"

export PVMBG_TOKEN="pvmbg.v1.change-me"
uvicorn app.main:app --host 0.0.0.0 --port 8082 --reload
```

After activation, the prompt normally starts with `(.venv)`. Run the checks from that same
activated environment:

```powershell
pytest
ruff check app tests
```

## Configuration

The service reads configuration from environment variables and exits during startup when a value is invalid. It does not read a committed `.env` file directly; Docker Compose supplies the values.

| Variable | Required | Default | Validation |
|---|---:|---:|---|
| `PVMBG_TOKEN` | yes | - | Must not be empty or whitespace-only |
| `RESPONSE_DELAY_MIN_MS` | no | `500` | Integer greater than or equal to zero |
| `RESPONSE_DELAY_MAX_MS` | no | `3000` | Integer greater than or equal to the minimum |
| `NEW_EVENT_INTERVAL_SECONDS` | no | `10` | Positive integer |
| `LOG_LEVEL` | no | `INFO` | `DEBUG`, `INFO`, `WARNING`, `ERROR`, or `CRITICAL` |

The token is represented as a Pydantic secret so its value is masked in model representations.
