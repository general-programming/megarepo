import React from 'react';
import PropTypes from 'prop-types';
import CssBaseline from '@material-ui/core/CssBaseline';
import Typography from '@material-ui/core/Typography';
import { withStyles } from '@material-ui/core/styles';
import Pricing from './Pricing';

import { connect } from 'react-redux';

const styles = theme => ({
    '@global': {
        body: {
            backgroundColor: theme.palette.common.white,
        },
    },
    layout: {
        width: 'auto',
        marginLeft: theme.spacing.unit * 3,
        marginRight: theme.spacing.unit * 3,
        [theme.breakpoints.up(900 + theme.spacing.unit * 3 * 2)]: {
            width: 900,
            marginLeft: 'auto',
            marginRight: 'auto',
        },
        paddingBottom: theme.spacing.unit * 2,
    },
    heroContent: {
        maxWidth: 600,
        margin: '0 auto',
        padding: `${theme.spacing.unit * 8}px 0 ${theme.spacing.unit * 6}px`,
    },
});


function Home(props) {
    const { classes } = props;

    return (
        <React.Fragment>
            <CssBaseline />
            <main className={classes.layout}>
                {/* Hero unit */}
                <div className={classes.heroContent}>
                    <Typography component='h1' variant='h2' align='center' color='textPrimary' gutterBottom>
                        Hewwo!
                    </Typography>
                    <Typography variant='h6' align='center' color='textSecondary' component='p'>
                        if you&#39;re here, you know what you are doing. if you don&#39;t, ask your friendly furry
                    </Typography>
                </div>
                {/* End hero unit */}
                <Pricing />
                <Typography variant='caption' align='left' gutterBottom> 
                    * acts of god, misconfigured wireguard, unplugging wrong extension cord excluded 
                </Typography>
                <Typography variant='caption' align='left' gutterBottom>
                    ** if VNC is left unlocked, we can&#39;t guarantee someone won&#39;t wireguard your VM
                </Typography>
            </main>
        </React.Fragment>
    );
}

Home.propTypes = {
    classes: PropTypes.object.isRequired,
};

const mapStateToProps = state => {
    const { user } = state.authentication;
    return {
        user,
    };
};

export default connect(mapStateToProps)(withStyles(styles)(Home));