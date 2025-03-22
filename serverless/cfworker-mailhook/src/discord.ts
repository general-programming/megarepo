import { createHash } from 'node:crypto';

/**
 * Pushes email information to Discord webhook
 * 
 * @param mailImage - URL to the rendered email image
 * @param mailRaw - URL to the raw email content
 * @param subject - Email subject
 * @param sentFrom - Sender information
 * @param sentTo - Recipient information
 * @returns Promise with status code and response text
 */
export async function discordPush(
  mailImage: string,
  mailRaw: string,
  subject: string = "[Blank subject]",
  sentFrom: string = "[Blank sender]",
  sentTo?: string
): Promise<[number, string]> {
  // Sent to mail
  let sent: string;
  if (!sentTo) {
    sent = "Sent to an unknown email";
    sentTo = "Unknown";
  } else {
    sent = `Sent to ${sentTo}`;
  }

  // Gravatar URL
  const emailRegex = /([a-zA-Z0-9_.+-]+@[a-zA-Z0-9-]+\.[a-zA-Z0-9-.]+)/g;
  const emails = sentFrom.match(emailRegex);

  let fromHash: string;
  if (!emails) {
    fromHash = "00000000000000000000000000000000";
  } else {
    fromHash = createHash('md5')
      .update(emails[emails.length - 1].toLowerCase())
      .digest('hex');
  }

  const avatarUrl = `https://www.gravatar.com/avatar/${fromHash}?default=identicon&s=256`;

  // Get Discord webhook URL from environment
  const webhookUrl = process.env.DISCORD_WEBHOOK;
  if (!webhookUrl) {
    console.error("Discord webhook URL not configured");
    return [500, "Discord webhook URL not configured"];
  }

  try {
    const response = await fetch(webhookUrl, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        username: sentFrom,
        avatar_url: avatarUrl,
        embeds: [
          {
            // Ass bleach pink
            color: 0xFFB9EC,
            title: subject,
            url: mailRaw,
            author: { name: sentFrom, url: mailRaw },
            image: { url: mailImage, width: 1024 },
            footer: { text: sent }
          }
        ]
      })
    });

    const responseText = await response.text();
    return [response.status, responseText];
  } catch (error) {
    console.error("Error sending Discord webhook:", error);
    return [500, error instanceof Error ? error.message : String(error)];
  }
}