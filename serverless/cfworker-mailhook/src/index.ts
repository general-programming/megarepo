/**
 * Welcome to Cloudflare Workers! This is your first worker.
 *
 * - Run `wrangler dev src/index.ts` in your terminal to start a development server
 * - Open a browser tab at http://localhost:8787/ to see your worker in action
 * - Run `wrangler publish src/index.ts --name my-worker` to publish your worker
 *
 * Learn more at https://developers.cloudflare.com/workers/
 */

import * as PostalMime from 'postal-mime'
import { Address } from 'postal-mime'

export interface Env {
	// Example binding to KV. Learn more at https://developers.cloudflare.com/workers/runtime-apis/kv/
	// MY_KV_NAMESPACE: KVNamespace;
	//
	// Example binding to Durable Object. Learn more at https://developers.cloudflare.com/workers/runtime-apis/durable-objects/
	// MY_DURABLE_OBJECT: DurableObjectNamespace;
	//
	// Example binding to R2. Learn more at https://developers.cloudflare.com/workers/runtime-apis/r2/
	// MY_BUCKET: R2Bucket;
}

// Mail endpoint handler
const MAIL_ENDPOINT = 'https://mailhook.generalprogramming.org/inbound'

function formatEmail(address: Address): string {
	return `${address.name} <${address.address}>`
}

async function handleEmail(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<void> {
	const parser = new PostalMime.default()

	// parse email content
	const rawEmailResposne = new Response(message.raw)
	const rawEmail = await rawEmailResposne.text()

	const email = await parser.parse(rawEmail)

	// generate the string for email to
	const toString = email.to.map(formatEmail).join(', ')
	const bccString = (email.bcc || []).map(formatEmail).join(', ')
	const ccString = (email.cc || []).map(formatEmail).join(', ')

	// send the email to the endpoint
	const emailBlob = new URLSearchParams({
		from: formatEmail(email.from),
		to: toString,
		subject: email.subject || "",
		email: rawEmail,
	});

	await fetch(MAIL_ENDPOINT, {
		method: 'POST',
		headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' },
		body: emailBlob,
	  });
}


export default {
	async email(message: ForwardableEmailMessage, env: Env, ctx: ExecutionContext): Promise<void> {
		await handleEmail(message, env, ctx)
	}
};
