# html-render-server
It renders HTML pages.

## Environ
**CDN_URL**: Domain that is CNAMEd to a B2 bucket.
**S3_ENDPOINT**: S3 compatible endpoint URL.
**S3_BUCKET**: Bucket to use for rendered pages..
**S3_ACCESS_KEY**: S3 access key
**SE_SECRET_KEY**: S3 secret key

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
