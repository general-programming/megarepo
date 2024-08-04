import { InteractionResponseFlags, InteractionResponseType } from 'discord-interactions';
import { TwilioConfig } from './objects';
import { convertNumberToE164, createEmbed, createField, getCommand } from './util';

import { Embed, EmbedFields } from './util';
import { Twilio } from './twilio';

import { ApplicationCommandOptionType } from 'discord-api-types/v10';

type CommandHandler = (body: any, options: any, guildConfig: DurableObjectStub<TwilioConfig>) => Promise<Embed>;

type CommandInteractionResponse = {
	id: string;
	applciation_id: string;
	type: InteractionResponseType;
	data?: CommandInterationData;

	guild_id?: string;
	// rest of the fields are truncated for sanity's sake
};

type CommandInterationData = {
	id: string;
	name: string;
	options?: CommandInteractionDataOption[];
	guild_id?: string;
	target_id?: string;
};

type CommandInteractionDataOption = {
	name: string;
	type: number;
	value: string;
	options?: CommandInteractionDataOption[];
	focused?: boolean;
};

const commandOptionsToDict = (options: CommandInteractionDataOption[]) => {
	const result = {};

	for (const option of options) {
		if (option.type == ApplicationCommandOptionType.Subcommand && option.options) {
			result[option.name] = commandOptionsToDict(option.options);
		} else {
			result[option.name] = option.value;
		}
	}

	return result;
};

export const EPHERMAL_COMMANDS = ['twilioconfig.token'];

const handleTokenUpdate = async (body: any, options: any, guildConfig: DurableObjectStub<TwilioConfig>) => {
	const sid = options.sid;
	const token = options.token;
	const twilio = new Twilio(sid, token);

	if (await twilio.verifyToken()) {
		guildConfig.setToken(sid, token);
		return createEmbed('Success', 'Twilio token updated', 'success');
	} else {
		return createEmbed('Error', 'Twilio token is invalid', 'failure');
	}
};

const handleNumberUpdate = async (body: any, options: any, guildConfig: DurableObjectStub<TwilioConfig>) => {
	const phoneNumber = options.number.phone_number;
	const e164PhoneNumber = convertNumberToE164(phoneNumber);
	const token = await guildConfig.getToken();
	const twilio = new Twilio(token.sid, token.token);

	if (!twilio) {
		return createEmbed('Error', 'Twilio token is not configured', 'failure');
	}

	try {
		for (const number of await twilio.incomingPhoneNumbers()) {
			if (number.phone_number === e164PhoneNumber) {
				await guildConfig.setNumber(e164PhoneNumber);
				return createEmbed('Success', `Phone number updated to ${e164PhoneNumber}`, 'success');
			}
		}
	} catch (error) {
		return createEmbed('Error', `Failed to verify phone number: ${error.toString()}`, 'failure');
	}

	return createEmbed('Error', `Phone number ${e164PhoneNumber} cannot be found in thw Twilio account.`, 'failure');
};

const handleSMS = async (body: any, options: any, guildConfig: DurableObjectStub<TwilioConfig>) => {
	// get twilio client + number first
	const token = await guildConfig.getToken();
	const twilio = new Twilio(token.sid, token.token);
	const phoneNumber = await guildConfig.getNumber();

	if (!twilio) {
		return createEmbed('Error', 'Twilio token is not configured', 'failure');
	}

	if (!phoneNumber) {
		return createEmbed('Error', 'Phone number is not configured', 'failure');
	}

	// get args next
	const toPhoneNumber = options.phone_number;
	const message = options.message;
	const attachmentId = options.attachment;
	let attachmentUrl: string = '';

	try {
		console.log(body.data);
		attachmentUrl = body.data.resolved.attachments[attachmentId].url;
	} catch (error) {
		attachmentUrl = '';
	}

	await sendSMS(twilio, phoneNumber, toPhoneNumber, message, attachmentUrl);
};

export const sendSMS = async (twilio: Twilio, phoneNumber: string, toPhoneNumber: string, message: string, attachmentUrl?: string) => {
	if (!toPhoneNumber) {
		return createEmbed('Error', 'No phone number provided', 'failure');
	}

	if (!message && !attachmentUrl) {
		return createEmbed('Error', 'No message or attachment is provided', 'failure');
	}

	try {
		const fields: EmbedFields[] = [createField('From', phoneNumber)];

		if (message) {
			fields.push(createField('Message', message));
		}

		await twilio.sendMessage(phoneNumber, toPhoneNumber, message, attachmentUrl);

		return createEmbed('Success', `Message sent to ${toPhoneNumber}`, 'success', attachmentUrl, fields);
	} catch (error) {
		console.error('handle sms error', error);
		return createEmbed('Error', `Failed to send message to ${toPhoneNumber}: ${error.toString()}`, 'failure');
	}
};

const parseCommand = async (body: CommandInteractionResponse, env: Env) => {
	const command = getCommand(body);

	// handle malformed body
	if (!body.data || !body.guild_id) {
		console.error('malformed body', body);
		return createEmbed('Error', 'request body missing data or guild_id', 'failure');
	}

	const options = commandOptionsToDict(body.data.options);
	let handler: CommandHandler | null = null;

	// Get the twilio config based on guild
	const guildID = body.guild_id;
	const guildConfigID: DurableObjectId = env.TWILIO_CONFIG.idFromName(guildID);
	const guildConfig = env.TWILIO_CONFIG.get(guildConfigID);

	switch (command) {
		case 'twilioconfig.token':
			handler = handleTokenUpdate;
			break;
		case 'twilioconfig.number':
			handler = handleNumberUpdate;
			break;
		case 'sms':
			handler = handleSMS;
			break;
	}

	let embed: Embed;
	if (handler) {
		try {
			embed = await handler(body, options, guildConfig);
		} catch (error) {
			console.error('handler error', error);
			embed = createEmbed('Error', error.toString(), 'failure');
		}
	} else {
		embed = createEmbed('Error', `Command ${command} is not supported`, 'failure');
	}

	let response = {
		tts: false,
		embeds: [embed],
		allowed_mentions: {},
	};

	if (EPHERMAL_COMMANDS.includes(command)) {
		response.flags = InteractionResponseFlags.EPHEMERAL;
	}

	return response;
};

export const handleCommand = async (body: any, env: Env) => {
	const interactionToken = body.token;
	const applicationId = body.application_id;

	const responseURL = `https://discord.com/api/webhooks/${applicationId}/${interactionToken}`;
	const responseBody = JSON.stringify(await parseCommand(body, env));

	const response = await fetch(responseURL, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
		},
		body: responseBody,
	});
	const responseText = await response.text();

	if (!response.ok) {
		console.error('send to discord error', responseText);
	}
};
