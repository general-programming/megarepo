import asyncio
import tempfile
import json
import hashlib
import os
import logging
import functools

from concurrent.futures import ThreadPoolExecutor

import aiofiles
from aiohttp import web
from b2blaze import B2
from pyppeteer import launch as launch_pyppeteer

# Logging configuration
if "DEBUG_FLOOD" in os.environ:
    logging.basicConfig(level=logging.DEBUG)
else:
    logging.basicConfig(level=logging.INFO)

logging.getLogger("asyncio").setLevel(logging.DEBUG)
log = logging.getLogger(__name__)

# TODO: Move these constants somewhere.
HEIGHT_PADDING = 25
CDN_BASE = "https://" + os.environ["CDN_URL"] + "/file/" + os.environ["B2_BUCKET"] + "/mailrender_api/"

# B2 utility
b2 = B2()
bucket = b2.buckets.get(os.environ["B2_BUCKET"])

# Service
routes = web.RouteTableDef()
executor = ThreadPoolExecutor(max_workers=4)

@routes.get('/')
async def rootpage(request):
    return web.Response(text="mail render api")

async def upload_file(data: bytes, file_name: str):
    loop = asyncio.get_running_loop()

    return await loop.run_in_executor(executor, functools.partial(bucket.files.upload, contents=data, file_name=file_name))

@routes.post('/render')
async def render_post(request):
    # Parse JSON and check auth.
    try:
        data = await request.json()
    except json.JSONDecodeError:
        return web.json_response({"error": "bad_post_or_json"})

    if data.get("auth", "") != "jesus2018":
        return web.json_response({"error": "bad_auth"})

    # Check for the HTML in data by popping it.
    try:
        page_html = data["html"]
    except KeyError:
        return web.json_response({"error": "bad_json_html"})

    # Encode the HTML back to UTF8.
    try:
        page_html = page_html.encode('utf-8')
    except UnicodeEncodeError:
        pass

    # Hash it for the filename.
    page_hash = hashlib.md5(page_html).hexdigest()

    browser = await launch_pyppeteer(
        executablePath="/usr/bin/chromium-browser",
        args=["--disable-dev-shm-usage", "--no-sandbox", "--disable-setuid-sandbox"]
    )
    page = await browser.newPage()

    with tempfile.TemporaryDirectory() as tmpdirname:
        tmphtml_path = os.path.join(tmpdirname, page_hash + ".html")
        rendered_path = os.path.join(tmpdirname, "rendered.jpg")

        with open(tmphtml_path, "wb") as tmphtml_handle:
            tmphtml_handle.write(page_html)

        await page.goto("file://" + tmphtml_path)

        real_height = await page.evaluate('''() => {
            let body = document.body,
                html = document.documentElement;

            return Math.max(
                body.scrollHeight,
                body.offsetHeight, 
                html.clientHeight,
                html.scrollHeight,
                html.offsetHeight
            )
        }''') + HEIGHT_PADDING

        await page.setViewport({
            "width": 1024,
            "height": real_height
        })

        await page.screenshot({
            'path': rendered_path,
            "clip": {
                "x": 0,
                "y": 0,
                "width": 1024,
                "height": real_height
            }
        })

        await browser.close()

        # Upload the raw HTML and the screenshot.
        await upload_file(page_html, f"mailrender_api/{page_hash}.html")
        async with aiofiles.open(rendered_path, "rb") as image:
            image_data = await image.read()
            await upload_file(image_data, f"mailrender_api/{page_hash}.jpg")

        return web.json_response({
            "image_url": CDN_BASE + page_hash + ".jpg",
            "raw_url": CDN_BASE + page_hash + ".html"
        })

app = web.Application(
    client_max_size=1024*1024*16  # Default of 2MB crippled some emails. 16MB should be "enough"
)
app.add_routes(routes)

log.info("Render API started!")

web.run_app(app)
