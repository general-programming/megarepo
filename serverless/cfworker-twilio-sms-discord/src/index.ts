import { verifyKey, InteractionType, InteractionResponseType, InteractionResponseFlags, MessageComponentTypes } from 'discord-interactions';

import { getCommand } from './util';
import { EPHERMAL_COMMANDS, handleCommand, sendSMS } from './command';

import { DurableObject } from 'cloudflare:workers';
import { Twilio } from './twilio';

/**
 * Welcome to Cloudflare Workers! This is your first Durable Objects application.
 *
 * - Run `npm run dev` in your terminal to start a development server
 * - Open a browser tab at http://localhost:8787/ to see your Durable Object in action
 * - Run `npm run deploy` to publish your application
 *
 * Bind resources to your worker in `wrangler.toml`. After adding bindings, a type definition for the
 * `Env` object can be regenerated with `npm run cf-typegen`.
 *
 * Learn more at https://developers.cloudflare.com/durable-objects
 */

/**
 * Associate bindings declared in wrangler.toml with the TypeScript type system
 */
export interface Env {
	DISCORD_PUB_KEY: '8fea7e6bebcc5b78ae79bc6520d2127b2468440d2fa839c338e74914ca642f9b';
	// Example binding to KV. Learn more at https://developers.cloudflare.com/workers/runtime-apis/kv/
	// MY_KV_NAMESPACE: KVNamespace;
	//
	// Example binding to Durable Object. Learn more at https://developers.cloudflare.com/workers/runtime-apis/durable-objects/
	TWILIO_CONFIG: DurableObjectNamespace<TwilioConfig>;
	//
	// Example binding to R2. Learn more at https://developers.cloudflare.com/workers/runtime-apis/r2/
	// MY_BUCKET: R2Bucket;
	//
	// Example binding to a Service. Learn more at https://developers.cloudflare.com/workers/runtime-apis/service-bindings/
	// MY_SERVICE: Fetcher;
	//
	// Example binding to a Queue. Learn more at https://developers.cloudflare.com/queues/javascript-apis/
	// MY_QUEUE: Queue;
}

export class TwilioConfig extends DurableObject {
	/**
	 * The constructor is invoked once upon creation of the Durable Object, i.e. the first call to
	 * 	`DurableObjectStub::get` for a given identifier (no-op constructors can be omitted)
	 *
	 * @param ctx - The interface for interacting with Durable Object state
	 * @param env - The interface to reference bindings declared in wrangler.toml
	 */
	constructor(ctx: DurableObjectState, env: Env) {
		super(ctx, env);
	}

	// phone number
	async setNumber(phoneNumber: string) {
		await this.ctx.storage.put('phone_number', phoneNumber);
	}

	async getNumber(): Promise<string | null> {
		return await this.ctx.storage.get('phone_number');
	}

	// token
	async getToken(): Promise<{ sid: string; token: string }> {
		return {
			sid: await this.ctx.storage.get('sid'),
			token: await this.ctx.storage.get('token'),
		};
	}

	async setToken(sid: string, token: string) {
		await this.ctx.storage.put('sid', sid);
		await this.ctx.storage.put('token', token);
	}
}

export default {
	/**
	 * Ingress handler for Discord interactions
	 *
	 * @param request - The request submitted to the Worker from the client
	 * @param env - The interface to reference bindings declared in wrangler.toml
	 * @param ctx - The execution context of the Worker
	 * @returns The response to be sent back to the client
	 */
	async fetch(request, env, ctx): Promise<Response> {
		// Verify the request signature
		const signature = request.headers.get('X-Signature-Ed25519');
		const timestamp = request.headers.get('X-Signature-Timestamp');
		const rawBody = await request.text();
		const validRequest = await verifyKey(rawBody, signature as string, timestamp as string, env.DISCORD_PUB_KEY);

		// Check if the request is valid
		if (!validRequest) {
			return new Response(
				JSON.stringify({
					error: 'Invalid request signature',
				}),
				{ status: 500, headers: { 'Content-Type': 'application/json' } }
			);
		}

		const body = JSON.parse(rawBody);
		console.log(body);

		if (body.type === InteractionType.PING) {
			console.log('got ping from discord');
			return new Response(
				JSON.stringify({
					type: InteractionResponseType.PONG,
				}),
				{ status: 200, headers: { 'Content-Type': 'application/json' } }
			);
		} else if (body.type === InteractionType.APPLICATION_COMMAND) {
			const data = {};

			if (EPHERMAL_COMMANDS.includes(getCommand(body))) {
				data.flags = InteractionResponseFlags.EPHEMERAL;
			}

			const response = JSON.stringify({
				type: InteractionResponseType.DEFERRED_CHANNEL_MESSAGE_WITH_SOURCE,
				data: data,
			});

			ctx.waitUntil(handleCommand(body, env));

			return new Response(response, { status: 200, headers: { 'Content-Type': 'application/json' } });
		} else if (body.type === InteractionType.MESSAGE_COMPONENT) {
			if (body.data?.custom_id === 'reply') {
				const smsNumber = body.message.embeds[0].title;

				const response = JSON.stringify({
					// TODO: modal reply constant
					type: InteractionResponseType.MODAL,
					data: {
						custom_id: 'reply_modal',
						title: `Reply to ${smsNumber}`,
						components: [
							{
								type: MessageComponentTypes.ACTION_ROW,
								components: [
									{
										type: MessageComponentTypes.INPUT_TEXT,
										custom_id: 'reply_text',
										label: 'Reply',
										style: 2,
									},
								],
							},
						],
					},
				});

				return new Response(response, { status: 200, headers: { 'Content-Type': 'application/json' } });
			}
		} else if (body.type === InteractionType.MODAL_SUBMIT) {
			const modalId = body.data.custom_id;

			const guildConfigID: DurableObjectId = env.TWILIO_CONFIG.idFromName(body.guild_id);
			const guildConfig = env.TWILIO_CONFIG.get(guildConfigID);
			const token = await guildConfig.getToken();
			const twilio = new Twilio(token.sid, token.token);
			const phoneNumber = await guildConfig.getNumber();

			if (modalId === 'reply_modal') {
				const replyText = body.data.components[0].components[0].value;
				const toPhoneNumber = body.message.embeds[0].title;
				const senderId = body.member.user.id;

				const embedResponse = await sendSMS(twilio, senderId, phoneNumber, toPhoneNumber, replyText);

				let response = {
					type: InteractionResponseType.CHANNEL_MESSAGE_WITH_SOURCE,
					data: {
						tts: false,
						embeds: [embedResponse],
						allowed_mentions: {},
					},
				};

				return new Response(JSON.stringify(response), { status: 200, headers: { 'Content-Type': 'application/json' } });
			}
		}

		console.log('Unknown interaction type', body.type);
		return new Response(JSON.stringify({ error: 'Unsupported Interaction Type' }), { status: 500 });
	},
} satisfies ExportedHandler<Env>;
