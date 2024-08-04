export const verifyTwilioToken = (sid: string, token: string) => {
	const client = new Twilio(sid, token);

	try {
		client.incomingPhoneNumbers.list();
	} catch (error) {
		return false;
	}

	return true;
};

export class Twilio {
	constructor(public sid: string, public token: string) {
		this.sid = sid;
		this.token = token;
	}

	public async incomingPhoneNumbers() {
		const endpoint = `https://api.twilio.com/2010-04-01/Accounts/${this.sid}/IncomingPhoneNumbers.json`;

		const response = await fetch(endpoint, {
			method: 'GET',
			headers: {
				'Content-Type': 'application/x-www-form-urlencoded',
				Authorization: `Basic ${btoa(`${this.sid}:${this.token}`)}`,
			},
		});

		const json = await response.json();

		if (!response.ok) {
			throw new Error(json.message);
		}

		return json.incoming_phone_numbers;
	}

	public async verifyToken() {
		try {
			await this.incomingPhoneNumbers();
		} catch (error) {
			return false;
		}

		return true;
	}

	public async sendMessage(fromPhoneNumber: string, toPhoneNumber: string, message: string, attachmentUrl?: string) {
		const endpoint = `https://api.twilio.com/2010-04-01/Accounts/${this.sid}/Messages.json`;

		const payload = {
			From: fromPhoneNumber,
			To: toPhoneNumber,
		};

		if (message) {
			payload['Body'] = message;
		}

		if (attachmentUrl) {
			payload['MediaUrl'] = [attachmentUrl];
		}

		const response = await fetch(endpoint, {
			method: 'POST',
			headers: {
				'Content-Type': 'application/x-www-form-urlencoded',
				Authorization: `Basic ${btoa(`${this.sid}:${this.token}`)}`,
			},
			body: new URLSearchParams(payload).toString(),
		});

		const json = await response.json();

		if (!response.ok) {
			throw new Error(json.message);
		}
	}
}
