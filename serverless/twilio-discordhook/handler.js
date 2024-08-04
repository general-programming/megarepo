const axios = require("axios").default;
const FormData = require("form-data");
const FileType = require("file-type");

const CALL_FIELDS = {
    FromCity: "City",
    FromState: "State",
    FromCountry: "Country",
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
    const response = await axios({
        method: "GET",
        url: url,
        responseType: "arraybuffer",
    });

    return {
        extension: (await FileType.fromBuffer(response.data)).ext,
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
    const isCall = false; // TODO/XXX Some field does calls.

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
    if (isSMS) {
        username += " Message";
    } else if (isCall) {
        username += " Voice";
        title = `Incoming call from ${event.From}`;
        footer = `Call to ${event.To}`;
    }

    // Create the reply components
    const components = [
        {
            type: 1,
            components: [
                {
                    type: 2,
                    style: 5,
                    label: "Reply",
                    custom_id: "reply",
                },
            ],
        },
    ];

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
    const webhookURI = checkIfSMS(event)
        ? context.DISCORD_WEBHOOK_SMS
        : context.DISCORD_WEBHOOK_VOICE;

    for (const payload of payloads) {
        await axios({
            method: "POST",
            url: webhookURI,
            headers: {
                "Content-Type": `multipart/form-data; boundary=${payload._boundary}`,
            },
            data: payload,
        });

        // Nasty hack to sleep for 200ms in between requests.
        await new Promise((_) => setTimeout(_, 200));
    }

    callback();
};
