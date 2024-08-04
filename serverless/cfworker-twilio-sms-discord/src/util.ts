export const getCommand = (body: any): string => {
	let commandName = body.data.name;

	for (let option of body.data.options) {
		if (option.type === 1) {
			commandName = commandName + '.' + option.name;
		}
	}

	return commandName;
};

export type Embed = {
	title?: string;
	type?: string;
	description?: string;
	url?: string;
	timestamp?: string;
	color?: number;
	footer?: {
		text?: string;
		icon_url?: string;
	};
	image?: {
		url?: string;
	};
	thumbnail?: {
		url?: string;
	};
	author?: {
		name?: string;
		url?: string;
		icon_url?: string;
	};
	fields?: EmbedFields[];
};

export type EmbedFields = {
	name: string;
	value: string;
	inline?: boolean;
};

export const createEmbed = (
	title: string,
	description: string,
	status: string,
	image: string = '',
	fields: EmbedFields[] = [],
	footer_text: string = ''
): Embed => {
	let color: number;
	const result: Embed = {};

	if (status === 'success') {
		color = hexToInt('00FF00');
	} else if (status === 'failure') {
		color = hexToInt('FF0000');
	}

	result.title = title;
	result.description = description;
	result.color = color;

	if (footer_text) {
		result.footer = {
			text: footer_text,
		};
	}

	if (fields) {
		result.fields = fields;
	}

	if (image) {
		result.image = {
			url: image,
		};
	}

	return result;
};

export const hexToInt = (hex: string): number => {
	return parseInt(hex, 16);
};

export const convertNumberToE164 = (number: string): string => {
	// Remove any formatting
	number = number.replace('-', '');
	number = number.replace('(', '');
	number = number.replace(')', '');
	number = number.replace(' ', '');
	number = number.replace('+', '');

	return `+${number}`;
};

export const createField = (name: string, value: string, inline: boolean = false): EmbedFields => {
	return {
		name: name,
		value: value,
		inline: inline,
	};
};
