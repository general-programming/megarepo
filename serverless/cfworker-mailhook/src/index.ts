/**
 * Welcome to Cloudflare Workers! This is your first worker.
 *
 * - Run `wrangler dev src/index.ts` in your terminal to start a development server
 * - Open a browser tab at http://localhost:8787/ to see your worker in action
 * - Run `wrangler publish src/index.ts --name my-worker` to publish your worker
 *
 * Learn more at https://developers.cloudflare.com/workers/
 */

import PostalMime from 'postal-mime'
import puppeteer from "@cloudflare/puppeteer";
import { md5, formatEmail } from './utils';
import { HEIGHT_PADDING } from './consts';
import { discordPush } from './discord';

export interface Env {
	MYBROWSER: Fetcher;
	BUCKET_BASE: string;
	MY_BUCKET: R2Bucket;
	DISCORD_WEBHOOK?: string;
}

async function handleEmail(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<void> {
	// parse email content
	const rawEmailResposne = new Response(message.raw)
	const rawEmail = await rawEmailResposne.text()

	const email = await PostalMime.parse(rawEmail)

	// generate the string for email to
	const toString = (email.to || []).map(formatEmail).join(', ')
	const bccString = (email.bcc || []).map(formatEmail).join(', ')
	const ccString = (email.cc || []).map(formatEmail).join(', ')


	// render the email then store the image + html content, and email in an s3 endpoint
	if (!email.html && !email.text) {
		console.error("No email content found");
		return;
	}

	const toRender = email.html || email.text || "";
	const rendered = await renderEmail(env, toRender)
	// md5 the email content
	const page_hash = await md5(rawEmail);
	await env.MY_BUCKET.put(`htmlrender_api/${page_hash}.html`, toRender, {
		httpMetadata: {
			contentType: "text/html",
		},
	});
	await env.MY_BUCKET.put(`htmlrender_api/${page_hash}.png`, rendered, {
		httpMetadata: {
			contentType: "image/png",
		},
	});

	// Set up URLs for the stored content
	const bucketBaseUrl = env.BUCKET_BASE;
	const mailImageUrl = `${bucketBaseUrl}/htmlrender_api/${page_hash}.png`;
	const mailRawUrl = `${bucketBaseUrl}/htmlrender_api/${page_hash}.html`;

	// Push to Discord if webhook URL is configured
	if (env.DISCORD_WEBHOOK) {
		// Set the environment variable for the discord function
		process.env.DISCORD_WEBHOOK = env.DISCORD_WEBHOOK;
		
		const [pushStatus, pushResponse] = await discordPush(
			mailImageUrl,
			mailRawUrl,
			email.subject || "[Blank subject]",
			formatEmail(email.from),
			toString
		);

		if (pushStatus !== 204) {
			console.error("Error from Discord:", pushResponse);
		}
	}
}

/* 
 * Renders the email using puppeteer
 *
 * @param env - CF environment variables
 * @param email - HTML content of the email to rende
 *
 */
async function renderEmail(env: Env, email: string): Promise<Buffer<ArrayBufferLike>> {
	const browser = await puppeteer.launch(env.MYBROWSER);
	const page = await browser.newPage();
	await page.setContent(email);

	const realHeight: number = await page.evaluate(() => {
		let body = document.body;
		let html = document.documentElement;
  
		return Math.max(
			body.scrollHeight,
			body.offsetHeight,
			html.clientHeight,
			html.scrollHeight,
			html.offsetHeight
		);
	});
	const paddedHeight = realHeight + HEIGHT_PADDING;

	await page.setViewport({
		width: 1024,
		height: paddedHeight,
	});

	const image = await page.screenshot({ type: 'png' });
	await browser.close();

	return image;
}

export default {
	async email(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<void> {
		await handleEmail(message, env, ctx)
	}
};
