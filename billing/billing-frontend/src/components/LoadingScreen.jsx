/* eslint-disable max-len */
import React from 'react';
import PropTypes from 'prop-types';

import LinearProgress from '@material-ui/core/LinearProgress';
import Typography from '@material-ui/core/Typography';
import { withStyles } from '@material-ui/core/styles';

const styles = theme => ({
    '@global': {
        body: {
            backgroundColor: theme.palette.common.white,
        },
    },
    heroContent: {
        maxWidth: 800,
        margin: '0 auto',
        padding: `${theme.spacing.unit * 25}px 0 ${theme.spacing.unit * 6}px`,
    },
    progress: {
        margin: theme.spacing.unit * 3,
    },
});


function Home(props) {
    const { classes } = props;

    return (
        <React.Fragment>
            <main className={classes.layout}>
                <div className={classes.heroContent}>
                    <Typography variant="h3" align="center" component="p">
                        Loading
                    </Typography>
                    <LinearProgress className={classes.progress} />
                </div>
            </main>
        </React.Fragment>
    );
}

Home.propTypes = {
    // eslint-disable-next-line react/forbid-prop-types
    classes: PropTypes.object.isRequired,
};

export default withStyles(styles)(Home);
