import asyncio

from pyppeteer import launch

HEIGHT_PADDING = 25


async def main():
    browser = await launch()
    page = await browser.newPage()
    await page.goto("file:///tmp/test.html")

    real_height = (
        await page.evaluate("""() => {
        let body = document.body,
            html = document.documentElement;

        return Math.max(
            body.scrollHeight,
            body.offsetHeight, 
            html.clientHeight,
            html.scrollHeight,
            html.offsetHeight
        )
    }""")
        + HEIGHT_PADDING
    )

    await page.setViewport({"width": 1024, "height": real_height})
    await page.screenshot(
        {
            "path": "example.png",
            "clip": {"x": 0, "y": 0, "width": 1024, "height": real_height},
        }
    )
    await browser.close()


asyncio.get_event_loop().run_until_complete(main())
