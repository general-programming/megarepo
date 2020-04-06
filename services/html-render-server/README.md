# html-render-server
It renders HTML pages.

## Environ
**CDN_URL**: Domain that is CNAMEd to a B2 bucket.
**B2_BUCKET**: Backblaze B2 bucket name.
**B2_KEY_ID**: B2 Application ID
**B2_APPLICATION_KEY**: B2 API application key

## Development
```sh
# Setup venv
virtualenv env
source env/bin/activate

# Install deps
pip install -r requirements.txt

# Runs the server
python api.py
```
