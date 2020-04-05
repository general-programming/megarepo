export const handleResponse = (response) => {
    return response.text().then((text) => {
        const data = text && JSON.parse(text);
        if (!response.ok) {
            if (response.status === 401) {
                // Reload if we are logged out from the server.
                window.location.reload(true);
            }

            const error = ((data && data.message) || data.error) || response.statusText;
            return Promise.reject(error);
        }

        return data;
    });
};

export const test = () => {
    return 'it works';
};
