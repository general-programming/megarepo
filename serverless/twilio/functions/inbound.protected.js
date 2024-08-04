const axios = require("axios").default;
const FormData = require("form-data");

const CALL_FIELDS = {
    FromCity: "City",
    FromState: "State",
    FromCountry: "Country",
    FromZip: "ZIP",
};

/**
 * Determines if an event is an SMS event.
 * @param {*} event The Twilio event.
 * @returns {bool} True if it is an SMS.
 */
function checkIfSMS(event) {
    // Anything with a body or MMS images should be an SMS.
    return event.Body || event.NumMedia > 0;
}

/**
 * Fetches a URL and stores the content as a buffer.
 * @param {string} url
 */
async function getImage(url) {
    const FileType = await import("file-type");

    const response = await axios({
        method: "GET",
        url: url,
        responseType: "arraybuffer",
    });

    return {
        extension: (await FileType.fileTypeFromBuffer(response.data)).ext,
        data: response.data,
    };
}

/**
 * Builds payloads for sending to the Discord webhook.
 * @param event
 */
async function buildDiscordWebhookPayloads(event) {
    let username = "Twilio";
    let title = `${event.From}`;
    let description = "";
    let footer = `Sent to ${event.To}`;
    const recieved = new Date().getTime();
    const fields = [];
    const payloads = [];

    // Determine if this is a call, SMS, or MMS;
    const isSMS = checkIfSMS(event);
    const isCall = !isSMS; // XXX: Is this the best way to determine if the payload is a call?.

    // Call field mapping
    for (const callField in CALL_FIELDS) {
        if (event[callField]) {
            fields.push({
                name: CALL_FIELDS[callField],
                value: event[callField],
                inline: true,
            });
        }
    }

    // Handle SMS + MMS body
    if (isSMS) {
        description = event.Body;
        if (event.NumMedia > 0) {
            fields.push({
                name: "Attachments",
                value: event.NumMedia.toString(),
                inline: true,
            });
        }
    }

    /* Get the event type first. */
    console.log(event);
    if (isSMS) {
        username += " Message";
    } else if (isCall) {
        username += " Voice (Function)";
        title = `Incoming call from ${event.From}`;
        footer = `Call to ${event.To}`;
    }

    // Create the component payload.
    const components = [];
    if (isSMS) {
        components.push({
            type: 1,
            components: [
                {
                    type: 2,
                    custom_id: "reply",
                    label: "Reply",
                    style: 1,
                    emoji: {
                        name: "🗨️",
                    },
                },
            ],
        });
    }

    // Create the message payload.
    const messageForm = new FormData();
    const messagePayload = {
        username: username,
        embeds: [
            {
                title: title,
                description: description,
                color: 16777215,
                footer: {
                    text: footer,
                },
                fields: fields,
            },
        ],
        components: components,
    };
    console.log(JSON.stringify(messagePayload));
    messageForm.append("payload_json", JSON.stringify(messagePayload));
    payloads.push(messageForm);

    // Create payloads for each image.
    if (event.NumMedia > 0) {
        for (let i = 0; i < event.NumMedia; i++) {
            const imageForm = new FormData();
            const imagePayload = {
                username: username,
            };
            const imageData = await getImage(event[`MediaUrl${i}`]);
            const filename = `twilio-mms-${event.From}-${recieved}-${i}.${imageData.extension}`;

            imageForm.append("payload_json", JSON.stringify(imagePayload));
            imageForm.append("file", imageData.data, filename);
            payloads.push(imageForm);
        }
    }

    return payloads;
}

exports.handler = async function (context, event, callback) {
    const payloads = await buildDiscordWebhookPayloads(event);
    const isSMS = checkIfSMS(event);
    const channelID = isSMS
        ? context.DISCORD_CHANNEL_SMS
        : context.DISCORD_CHANNEL_VOICE;

    const postURI = `https://discord.com/api/channels/${channelID}/messages`;
    console.log("Posting to", postURI);

    for (const payload of payloads) {
        await axios({
            method: "POST",
            url: postURI,
            headers: {
                "Content-Type": `multipart/form-data; boundary=${payload._boundary}`,
                Authorization: `Bot ${context.DISCORD_BOT_TOKEN}`,
            },
            data: payload,
        }).catch((err) => {
            console.error("Got HTTP error posting to Discord", err);

            if (err.response) {
                console.error(err.response.data);
            }
        });

        // Nasty hack to sleep for 200ms in between requests.
        await new Promise((_) => setTimeout(_, 200));
    }

    if (isSMS) {
        callback();
    } else {
        const twiml = new Twilio.twiml.VoiceResponse();
        const dial = twiml.dial({
            callerId: event.From,
            record: "record-from-ringing-dual",
        });

        dial.sip(
            {
                username: context.SIP_USERNAME,
                password: context.SIP_PASSWORD,
            },
            `sip:${event.To}@${context.SIP_DOMAIN}`
        );
        console.log(twiml);
        return callback(null, twiml);
    }
};
