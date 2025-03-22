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
import { md5, formatEmail, toEnrich } from './utils';
import { HEIGHT_PADDING } from './consts';
import { discordPush } from './discord';

export interface Env {
	MYBROWSER: Fetcher;
	BUCKET_BASE: string;
	MY_BUCKET: R2Bucket;
	DISCORD_WEBHOOK?: string;
}

async function handleEmail(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<boolean> {
	// parse email content
	const rawEmailResposne = new Response(message.raw)
	const rawEmail = await rawEmailResposne.text()

	const email = await PostalMime.parse(rawEmail)

	// generate the string for email to
	const toString = (email.to || []).map(formatEmail).join(', ');

	// log
	console.log(`Received email from ${formatEmail(email.from)} to ${toString}. (Subject: ${email.subject})`);

	// render the email then store the image + html content, and email in an s3 endpoint
	if (!email.html && !email.text) {
		console.error("No email content found");
		return false;
	}

	// prep for rendering
	const toRender = toEnrich(email);
	// md5 the email content
	const page_hash = await md5(rawEmail);
	await env.MY_BUCKET.put(`htmlrender_api/${page_hash}.html`, toRender, {
		httpMetadata: {
			contentType: "text/html",
		},
	});

	// email render
	const rendered = await renderEmail(env, toRender);
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
	const [pushStatus, pushResponse] = await discordPush(
		env,
		mailImageUrl,
		mailRawUrl,
		email.subject || "[Blank subject]",
		formatEmail(email.from),
		toString
	);

	if (pushStatus !== 204) {
		console.error("Error from Discord:", pushResponse);
		return false;
	} else {
		console.log("Discord push successful, status code:", pushStatus);
	}

	return true;
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

	const image = await page.screenshot({
		type: 'png',
		fullPage: true,
	});
	await browser.close();

	return image;
}

export default {
	async email(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<void> {
		const result = handleEmail(message, env, ctx);
		return ctx.waitUntil(result);
	}
};
