/**
 * Constants for the mailhook worker
 */

// Mail endpoint handler
export const HEIGHT_PADDING = 25;

export const HEIGHT_SCRIPT = `
  () => {
    let body = document.body,
      html = document.documentElement;

    return Math.max(
      body.scrollHeight,
      body.offsetHeight,
      html.clientHeight,
      html.scrollHeight,
      html.offsetHeight
    )
  }()
`;